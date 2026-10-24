package domain

// Segment é um arquivo .wal do etcd: o número de sequência, o primeiro índice esperado
// nele (do nome %016x-%016x.wal) e o tamanho em bytes.
type Segment struct {
	Seq   uint64
	First Index
	Bytes int64
}

// RetentionView é tudo o que decide até onde o disco do log pode ser liberado (RT-15).
// Os segmentos vêm ordenados por Seq. MinPin == 0 quer dizer sem pino.
type RetentionView struct {
	Segments       []Segment
	AppliedDurable Index // último progresso com fsync (G1), nunca a marca d'água em memória
	Floor          Index // piso publicado pela líder: menor Match das réplicas na janela
	MinPin         Index // menor pino (leituras e reconstruções em curso)
	Snapshot       Index // índice do último snapshot
}

// Limit é o maior índice cujas entradas o disco pode descartar:
// min(aplicado durável, piso, pinos, snapshot).
func (v RetentionView) Limit() Index {
	l := min(v.AppliedDurable, v.Floor, v.Snapshot)
	if v.MinPin > 0 {
		l = min(l, v.MinPin)
	}
	return l
}

// RetainedSince soma os bytes retidos a partir do segmento que contém i: o último
// segmento com First ≤ i, ou o primeiro, se nenhum.
func (v RetentionView) RetainedSince(i Index) int64 {
	start := 0
	for k, s := range v.Segments {
		if s.First <= i {
			start = k
		}
	}
	var n int64
	for _, s := range v.Segments[start:] {
		n += s.Bytes
	}
	return n
}

// Removable devolve os segmentos que podem ser apagados com o limite dado: o segmento k sai
// quando o seguinte começa em índice ≤ limit (todas as entradas de k estão ≤ limit) e o
// seguinte não é o último. A cauda e o segmento anterior a ela nunca saem.
func (v RetentionView) Removable(limit Index) []Segment {
	var out []Segment
	for k := 0; k+1 < len(v.Segments)-1; k++ {
		if v.Segments[k+1].First > limit {
			break
		}
		out = append(out, v.Segments[k])
	}
	return out
}

// FirstRetained é o primeiro índice que ainda pode estar no disco.
func (v RetentionView) FirstRetained() Index {
	if len(v.Segments) == 0 {
		return v.Snapshot + 1
	}
	return v.Segments[0].First
}

// LeaderFloor calcula, na líder, o piso de retenção e quem está na janela (RT-15, DC-08):
// um par está na janela enquanto os bytes retidos desde o seu Match não passam de
// retainMax. O piso é o menor Match na janela; sem nenhum par na janela, é o snapshot.
//
// DC-20: a saída da janela não tem volta. Um par cujo Match + 1 já saiu do disco retido
// está fora, mesmo que os bytes contados a partir do primeiro segmento que sobrou caibam de
// novo em retainMax: o que ele precisa já foi apagado. Match 0 (ainda desconhecido, logo
// depois de uma eleição) não tem essa regra.
func LeaderFloor(match map[uint64]Index, self uint64, v RetentionView, retainMax int64) (Index, map[uint64]bool) {
	floor, found := v.Snapshot, false
	in := make(map[uint64]bool, len(match))
	first := v.FirstRetained()
	for id, m := range match {
		if id == self {
			continue
		}
		ok := v.RetainedSince(m) <= retainMax && (m == 0 || len(v.Segments) == 0 || m+1 >= first)
		in[id] = ok
		if ok && (!found || m < floor) {
			floor, found = m, true
		}
	}
	return floor, in
}

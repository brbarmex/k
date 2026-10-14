// Package replog é o log de replicação (ADR-055): um log segmentado simples, por shard,
// em volume próprio, derivado do log do Raft.
//
//   - Append grava SEM fsync, com uma escrita por lote. O log não está no caminho do ACK:
//     a durabilidade que conta para o ACK é o fsync do log do Raft.
//   - Sync é o checkpoint: um fsync do segmento ativo que publica o DurableIndex. Só até
//     ele o log do Raft pode ser compactado (domain.RaftCompactLimit). O fsync roda FORA
//     da trava do Append (ADR-059): o Append só espera o disco na rotação de segmento.
//   - CompactPrefix apaga segmentos inteiros: o disco volta na hora, sem compactação.
//   - Open trata a cauda rasgada do último segmento (trunca) e recusa corrupção no meio.
//
// Esboço: o formato do payload é JSON para ficar legível; o real é o binário da 6.3.
package replog

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

var (
	_ port.ReplicationLog = (*Log)(nil)
	_ port.ApplyProgress  = (*Log)(nil)
)

var (
	castagnoli = crc32.MakeTable(crc32.Castagnoli)
	// ErrGap: o lote não começa logo depois da cabeça. O log é derivado do Raft, então
	// um buraco é bug de quem chama, nunca algo para tolerar.
	ErrGap = errors.New("replog: lote fora de sequência")
	// ErrCorrupt: registro inválido fora da cauda do último segmento. O nó não sobe.
	ErrCorrupt = errors.New("replog: corrupção no meio do log")
)

const (
	headerLen    = 8 // len u32 | crc32c u32
	progressFile = "progress"
	progressLen  = 16 // índice u64 | ordem u32 | crc32c u32
)

type segment struct {
	first, last domain.Index // last == first-1 quando vazio
	path        string
	size        int64
}

// Log é o log de replicação de UM shard. Seguro para uso concorrente.
//
// Duas travas (ADR-059): mu protege o estado e é a única que o Append toma; syncMu deixa
// um checkpoint por vez e nunca é tomada por quem segura mu. Ordem: syncMu antes de mu.
type Log struct {
	mu       sync.Mutex
	syncMu   sync.Mutex
	dir      string
	maxSeg   int64
	segs     []*segment // ordenados; o último é o ativo
	active   *os.File
	retired  []*os.File // segmentos que saíram na rotação (já com fsync); o Sync fecha
	durable  domain.Index
	progress *os.File
	fsync    func(*os.File) error // (*os.File).Sync; os testes trocam para simular disco lento
}

// Open abre (ou cria) o log do shard em dir. maxSegment é o tamanho para rotacionar.
func Open(dir string, maxSegment int64) (*Log, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Log{dir: dir, maxSeg: maxSegment, fsync: (*os.File).Sync}
	names, err := filepath.Glob(filepath.Join(dir, "*.seg"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names) // nome = primeiro índice com zeros à esquerda
	for i, name := range names {
		var first uint64
		if _, err := fmt.Sscanf(filepath.Base(name), "%020d.seg", &first); err != nil {
			return nil, fmt.Errorf("replog: nome de segmento inválido %q", name)
		}
		seg := &segment{first: domain.Index(first), last: domain.Index(first) - 1, path: name}
		if err := l.scan(seg, i == len(names)-1); err != nil {
			return nil, err
		}
		if n := len(l.segs); n > 0 && seg.first != l.segs[n-1].last+1 {
			return nil, fmt.Errorf("%w: buraco entre segmentos em %d", ErrCorrupt, seg.first)
		}
		l.segs = append(l.segs, seg)
	}
	if len(l.segs) > 0 {
		if l.active, err = os.OpenFile(l.segs[len(l.segs)-1].path, os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
			return nil, err
		}
	}
	if l.progress, err = os.OpenFile(filepath.Join(dir, progressFile), os.O_RDWR|os.O_CREATE, 0o644); err != nil {
		return nil, err
	}
	// O que sobreviveu está no arquivo, mas pode estar só no cache de páginas (queda de
	// processo). Um fsync na abertura torna tudo durável antes de publicar o DurableIndex.
	if _, err := l.Sync(context.Background()); err != nil {
		return nil, err
	}
	return l, nil
}

// scan valida os registros do segmento. Na cauda do último segmento, registro curto ou
// com CRC errado é escrita incompleta: trunca ali. Em qualquer outro lugar é corrupção.
func (l *Log) scan(seg *segment, isLast bool) error {
	f, err := os.OpenFile(seg.path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var off int64
	for {
		e, n, err := readRecord(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			if !isLast {
				return fmt.Errorf("%w: %s no byte %d", ErrCorrupt, seg.path, off)
			}
			// Registros válidos depois do inválido indicariam corrupção, não queda.
			// O esboço confia no CRC e trunca; o real confere o resto do arquivo.
			if err := f.Truncate(off); err != nil {
				return err
			}
			break
		}
		if e.Index != seg.last+1 {
			return fmt.Errorf("%w: índice %d fora de ordem em %s", ErrCorrupt, e.Index, seg.path)
		}
		seg.last = e.Index
		off += n
	}
	seg.size = off
	return nil
}

func readRecord(r *bufio.Reader) (domain.Entry, int64, error) {
	var hdr [headerLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if err == io.EOF {
			return domain.Entry{}, 0, io.EOF
		}
		return domain.Entry{}, 0, io.ErrUnexpectedEOF
	}
	n := binary.LittleEndian.Uint32(hdr[0:4])
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return domain.Entry{}, 0, io.ErrUnexpectedEOF
	}
	if crc32.Checksum(payload, castagnoli) != binary.LittleEndian.Uint32(hdr[4:8]) {
		return domain.Entry{}, 0, errors.New("crc")
	}
	var e domain.Entry
	if err := json.Unmarshal(payload, &e); err != nil {
		return domain.Entry{}, 0, err
	}
	return e, int64(headerLen) + int64(n), nil
}

// Append grava o lote no fim do log, sem fsync e com UMA escrita por lote (ou uma por
// trecho, quando o lote atravessa a rotação). Entradas que o log já tem são ignoradas:
// na partida, o trecho vindo do Raft pode se sobrepor ao que sobreviveu (ADR-055, item 6).
// Nunca espera o checkpoint, só a rotação de segmento (ADR-059).
func (l *Log) Append(_ context.Context, batch []domain.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var buf []byte
	next := l.lastLocked() + 1 // só vale com segmento aberto
	for _, e := range batch {
		if len(l.segs) > 0 {
			if e.Index < next {
				continue
			}
			if e.Index != next {
				if err := l.flushLocked(&buf, next-1); err != nil {
					return err
				}
				return fmt.Errorf("%w: chegou %d, cabeça em %d", ErrGap, e.Index, next-1)
			}
		}
		if n := len(l.segs); n == 0 || l.segs[n-1].size+int64(len(buf)) >= l.maxSeg {
			if err := l.flushLocked(&buf, next-1); err != nil {
				return err
			}
			if err := l.rotateLocked(e.Index); err != nil {
				return err
			}
		}
		payload, err := json.Marshal(e)
		if err != nil {
			return err
		}
		var hdr [headerLen]byte
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(payload)))
		binary.LittleEndian.PutUint32(hdr[4:8], crc32.Checksum(payload, castagnoli))
		buf = append(append(buf, hdr[:]...), payload...)
		next = e.Index + 1
	}
	return l.flushLocked(&buf, next-1)
}

// flushLocked grava de uma vez o que foi montado para o segmento ativo; last é o índice
// da última entrada em buf.
func (l *Log) flushLocked(buf *[]byte, last domain.Index) error {
	if len(*buf) == 0 {
		return nil
	}
	if _, err := l.active.Write(*buf); err != nil {
		return err // o chamador trata como fatal (ADR-055, item 10)
	}
	seg := l.segs[len(l.segs)-1]
	seg.last = last
	seg.size += int64(len(*buf))
	*buf = (*buf)[:0]
	return nil
}

// rotateLocked abre um segmento novo começando em next. O segmento que sai é sincronizado
// aqui (só o ativo pode ter dados sem fsync) e vai para retired em vez de ser fechado: um
// Sync pode estar fazendo fsync nele agora, fora da trava, e é o Sync que o fecha (ADR-059).
func (l *Log) rotateLocked(next domain.Index) error {
	if l.active != nil {
		if err := l.fsync(l.active); err != nil {
			return err
		}
		l.retired = append(l.retired, l.active)
		l.active = nil
		l.durable = max(l.durable, l.lastLocked())
	}
	path := filepath.Join(l.dir, fmt.Sprintf("%020d.seg", uint64(next)))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	l.active = f
	l.segs = append(l.segs, &segment{first: next, last: next - 1, path: path})
	return syncDir(l.dir) // o nome do arquivo novo também precisa ser durável
}

// Sync é o checkpoint durável, chamado a cada REPLOG_SYNC_INTERVAL, fora do caminho do
// ACK. Sob a trava só se lê qual arquivo sincronizar e até que índice ele vai; o fsync
// roda sem a trava, e o Append continua gravando no cache de páginas enquanto o disco
// trabalha (ADR-059). Tudo até o índice lido está nesse arquivo ou em segmentos que já
// tiveram fsync na rotação, então o DurableIndex nunca passa do que está no disco.
func (l *Log) Sync(context.Context) (domain.Index, error) {
	l.syncMu.Lock()
	defer l.syncMu.Unlock()
	return l.syncLocked()
}

// syncLocked exige syncMu.
func (l *Log) syncLocked() (domain.Index, error) {
	l.mu.Lock()
	f, upTo := l.active, l.lastLocked()
	l.mu.Unlock()
	if f != nil {
		if err := l.fsync(f); err != nil {
			return l.DurableIndex(), err // fatal para o processo: não se repete fsync
		}
	}
	l.mu.Lock()
	l.durable = max(l.durable, upTo)
	d, retired := l.durable, l.retired
	l.retired = nil
	l.mu.Unlock()
	var err error
	for _, r := range retired {
		err = errors.Join(err, r.Close())
	}
	return d, err
}

// DurableIndex é o maior índice coberto pelo último Sync.
func (l *Log) DurableIndex() domain.Index {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.durable
}

// LastIndex é o maior índice gravado, durável ou não.
func (l *Log) LastIndex() domain.Index {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastLocked()
}

func (l *Log) lastLocked() domain.Index {
	if len(l.segs) == 0 {
		return 0
	}
	return l.segs[len(l.segs)-1].last
}

// Read devolve as entradas de from a to, inclusive: catch-up da Azure, replay, drctl.
func (l *Log) Read(_ context.Context, from, to domain.Index) ([]domain.Entry, error) {
	l.mu.Lock()
	segs := make([]segment, len(l.segs)) // cópia: o Append muda last e size do ativo
	for i, s := range l.segs {
		segs[i] = *s
	}
	l.mu.Unlock()
	var out []domain.Entry
	for _, s := range segs {
		if s.last < from || s.first > to || s.last < s.first {
			continue
		}
		f, err := os.Open(s.path)
		if err != nil {
			return nil, err
		}
		r := bufio.NewReader(io.LimitReader(f, s.size))
		for {
			e, _, err := readRecord(r)
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return nil, err
			}
			if e.Index >= from && e.Index <= to {
				out = append(out, e)
			}
		}
		f.Close()
	}
	return out, nil
}

// CompactPrefix apaga os segmentos inteiramente até upTo. O segmento ativo nunca é
// apagado. Granularidade de segmento: o espaço volta na hora, sem compactação.
func (l *Log) CompactPrefix(_ context.Context, upTo domain.Index) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := l.segs[:0]
	for i, s := range l.segs {
		if i < len(l.segs)-1 && s.last <= upTo {
			if err := os.Remove(s.path); err != nil {
				return err
			}
			continue
		}
		keep = append(keep, s)
	}
	l.segs = keep
	return nil
}

// TruncateSuffix existe pela interface; o log só recebe entradas commitadas, então não
// há sufixo a desfazer. Chamar é erro de quem chama.
func (l *Log) TruncateSuffix(context.Context, domain.Index) error {
	return errors.New("replog: o log de replicação só tem entradas commitadas")
}

// SaveProgress grava o progresso do applier, sem fsync: a marca d'água (índice, ordem)
// até a qual tudo está no banco (ADR-048, ADR-058). Perdido numa queda de host, o replay
// só fica mais longo.
func (l *Log) SaveProgress(idx domain.Index, ord uint32) error {
	var b [progressLen]byte
	binary.LittleEndian.PutUint64(b[0:8], uint64(idx))
	binary.LittleEndian.PutUint32(b[8:12], ord)
	binary.LittleEndian.PutUint32(b[12:16], crc32.Checksum(b[:12], castagnoli))
	_, err := l.progress.WriteAt(b[:], 0)
	return err
}

// LoadProgress devolve (0, 0) se o registro não existe ou não confere: o applier recomeça
// do início do que está retido, de forma idempotente.
func (l *Log) LoadProgress() (domain.Index, uint32, error) {
	var b [progressLen]byte
	if _, err := l.progress.ReadAt(b[:], 0); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if crc32.Checksum(b[:12], castagnoli) != binary.LittleEndian.Uint32(b[12:16]) {
		return 0, 0, nil
	}
	return domain.Index(binary.LittleEndian.Uint64(b[0:8])), binary.LittleEndian.Uint32(b[8:12]), nil
}

// Close sincroniza e fecha.
func (l *Log) Close() error {
	l.syncMu.Lock()
	defer l.syncMu.Unlock()
	_, err := l.syncLocked()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != nil {
		err = errors.Join(err, l.active.Close())
		l.active = nil
	}
	return errors.Join(err, l.progress.Close())
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"example.com/datareplication/internal/domain"
)

// Config é lido uma vez na partida e é imutável (12 Factors, III). Sem hot reload.
type Config struct {
	CellID    string // env CELL_ID (obrigatória)
	NodeID    uint64 // env NODE_ID (obrigatória, > 0; na POC igual ao CELL_ID)
	APIAddr   string // env API_ADDR
	PeerAddr  string // env PEER_ADDR
	AdminAddr string // env ADMIN_ADDR
	RaftDir   string // env RAFT_DIR: subdiretório do volume (C-04)

	// Membros do shard: id → endereço gRPC. Iguais em todas as células (RT-03).
	Peers map[uint64]string // env PEERS: votantes

	Tables         []string // env TABLES: nomes lógicos com {cell}
	DynamoEndpoint string   // env DYNAMODB_ENDPOINT: vazio = AWS real
	AWSRegion      string   // env AWS_REGION

	TableWait           time.Duration // TABLE_WAIT: espera pelas tabelas na partida; padrão 120s
	GatePublishInterval time.Duration // GATE_PUBLISH_INTERVAL: padrão 100ms (G-06)
	ReplSyncMax         time.Duration // REPL_SYNC_MAX: atraso máximo de replicação de uma célula sincronizada (DC-13); padrão 2s

	// ApplyWorkers (APPLY_WORKERS) é quantos itens o applier grava em paralelo no banco da
	// célula, com a ordem garantida por chave (ADR-058). Padrão 16; 1 = um item por vez.
	ApplyWorkers int
	// ApplyLagMax (APPLY_LAG_MAX) é quantas entradas o apply local pode ficar atrás do gate
	// antes de a líder recusar escritas novas com ThrottlingException (ADR-060). Padrão
	// APPLY_WORKERS x 256 (o que cabe nas filas dos workers).
	ApplyLagMax uint64
	// ElectionTicks (ELECTION_TICKS) é o timeout de eleição em ticks de 100 ms (ADR-060).
	// Padrão 10 (1 s). Na POC em notebook o compose usa 30 (3 s). Entra no RTO.
	ElectionTicks int

	// Orçamento de tempo (ver 9.4 do documento base e ADR-028). Opcionais, com padrão.
	ClientTimeout   time.Duration // CLIENT_TIMEOUT: menor timeout do SDK dos clientes; padrão 30s
	RequestDeadline time.Duration // REQUEST_DEADLINE: prazo de TODA requisição; padrão ClientTimeout/4 (7,5 s)
	WaitLeader      time.Duration // WAIT_LEADER: espera por eleição antes do 503 (ADR-039); padrão 1s
	ForwardTimeout  time.Duration // FORWARD_TIMEOUT: limite do encaminhamento à líder; padrão 3s
	IdleTimeout     time.Duration // IDLE_TIMEOUT: conexão ociosa; padrão 120s (> idle dos SDKs)

	// Compactação do log do Raft (S.6 da trecho, O-30). Os tamanhos aceitam sufixos
	// (64MiB, 2GiB) pelo domain.ParseSize.
	ClusterID            string        // CLUSTER_ID (obrigatória): igual nas 3 células (RT-20)
	SnapshotEveryEntries uint64        // SNAPSHOT_EVERY_ENTRIES: entradas entre snapshots (RT-11)
	SnapshotEveryBytes   int64         // SNAPSHOT_EVERY_BYTES: bytes na RAM entre snapshots (RT-11)
	RaftCatchupEntries   uint64        // RAFT_CATCHUP_ENTRIES: cauda mantida na RAM atrás do snapshot (RT-14)
	RaftRAMMax           int64         // RAFT_RAM_MAX: teto de bytes no MemoryStorage (RT-14, SG-23)
	RaftCompactInterval  time.Duration // RAFT_COMPACT_INTERVAL: ciclo do Compactor
	RetainMaxBytes       int64         // RETAIN_MAX_BYTES: janela da réplica em bytes retidos (RT-15)
	RetainDiskHard       float64       // RETAIN_DISK_HARD: teto duro como fração do volume (RT-15)
	RetainDiskHardBytes  int64         // RETAIN_DISK_HARD_BYTES: teto duro absoluto; 0 = desligado (DC-07)
	WalSegmentBytes      int64         // WAL_SEGMENT_BYTES: tamanho do segmento do WAL (DC-07)
	RebuildRetry         time.Duration // REBUILD_RETRY: espera entre tentativas de reconstrução (AP-18)
	RebuildPinTTL        time.Duration // REBUILD_PIN_TTL: validade do pino da reconstrução na fonte (DC-10)
	PeerQueueBytes       int64         // PEER_QUEUE_BYTES: teto em bytes da fila de saída por par (DC-09)
}

// RemovedModeVars são as variáveis do antigo modo Eventual (ADR-057).
var RemovedModeVars = []string{"READ_MODE_DEFAULT", "WRITE_MODE", "MAX_STALENESS"}

// MaxApplyWorkers limita APPLY_WORKERS: cada worker é uma conexão ao banco (ADR-058).
const MaxApplyWorkers = 256

// MaxApplyLagMax limita APPLY_LAG_MAX: acima disso a admissão não protege nada.
const MaxApplyLagMax = 10_000_000

// MinElectionTicks e MaxElectionTicks limitam ELECTION_TICKS (o heartbeat é 1 tick).
const (
	MinElectionTicks = 3
	MaxElectionTicks = 100
)

// MinIdleTimeout é o maior idle de SDK conhecido (Go, 90 s). O servidor precisa ficar
// acima, senão o SDK reusa uma conexão que o servidor já fechou.
const MinIdleTimeout = 90 * time.Second

// Tamanhos de referência das faixas da compactação (O-30).
const (
	kib = int64(1) << 10
	mib = int64(1) << 20
	gib = int64(1) << 30
	tib = int64(1) << 40

	// uncommittedMax é o MaxUncommittedEntriesSize do Raft (32 MiB): o RAFT_RAM_MAX precisa
	// caber o gatilho do snapshot mais as entradas ainda não commitadas.
	uncommittedMax = 32 * mib
)

// clusterIDRe é o formato do CLUSTER_ID (RT-20).
var clusterIDRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// Load lê o ambiente do processo com o viper.
func Load() (Config, error) {
	v := viper.New()
	v.AutomaticEnv()
	return LoadFrom(v.GetString)
}

// LoadFrom lê de qualquer fonte (testes). Reúne TODOS os erros: o processo não sobe se
// faltar ou estiver incoerente qualquer valor.
func LoadFrom(getenv func(string) string) (Config, error) {
	var errs []error
	c := Config{
		CellID:         getenv("CELL_ID"),
		APIAddr:        orDefault(getenv("API_ADDR"), ":8000"),
		PeerAddr:       orDefault(getenv("PEER_ADDR"), ":7000"),
		AdminAddr:      orDefault(getenv("ADMIN_ADDR"), ":9090"),
		RaftDir:        orDefault(getenv("RAFT_DIR"), "/var/lib/dr/raft/wal"),
		DynamoEndpoint: getenv("DYNAMODB_ENDPOINT"),
		AWSRegion:      getenv("AWS_REGION"),
	}
	dur := func(key string, def time.Duration, required bool) time.Duration {
		s := getenv(key)
		if s == "" {
			if required {
				errs = append(errs, errors.New("variável obrigatória ausente: "+key))
			}
			return def
		}
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s inválida %q: duração positiva (ex.: 30s)", key, s))
			return def
		}
		return d
	}
	c.ClientTimeout = dur("CLIENT_TIMEOUT", 30*time.Second, false)
	c.RequestDeadline = dur("REQUEST_DEADLINE", c.ClientTimeout/4, false)
	c.WaitLeader = dur("WAIT_LEADER", time.Second, false)
	c.ForwardTimeout = dur("FORWARD_TIMEOUT", 3*time.Second, false)
	c.IdleTimeout = dur("IDLE_TIMEOUT", 120*time.Second, false)
	c.TableWait = dur("TABLE_WAIT", 120*time.Second, false)
	c.GatePublishInterval = dur("GATE_PUBLISH_INTERVAL", 100*time.Millisecond, false)
	c.ReplSyncMax = dur("REPL_SYNC_MAX", 2*time.Second, false)
	if c.ReplSyncMax < 2*c.GatePublishInterval || c.ReplSyncMax > time.Minute {
		errs = append(errs, errors.New("REPL_SYNC_MAX deve ficar entre 2 × GATE_PUBLISH_INTERVAL e 60s"))
	}

	c.ApplyWorkers = 16
	if s := getenv("APPLY_WORKERS"); s != "" {
		if n, err := strconv.Atoi(s); err != nil || n < 1 || n > MaxApplyWorkers {
			errs = append(errs, fmt.Errorf("APPLY_WORKERS inválida %q: inteiro de 1 a %d", s, MaxApplyWorkers))
		} else {
			c.ApplyWorkers = n
		}
	}

	c.ApplyLagMax = uint64(c.ApplyWorkers) * 256
	if s := getenv("APPLY_LAG_MAX"); s != "" {
		if n, err := strconv.ParseUint(s, 10, 64); err != nil || n < 1 || n > MaxApplyLagMax {
			errs = append(errs, fmt.Errorf("APPLY_LAG_MAX inválida %q: inteiro de 1 a %d", s, MaxApplyLagMax))
		} else {
			c.ApplyLagMax = n
		}
	}
	c.loadCompaction(getenv, &errs)

	c.ElectionTicks = 10
	if s := getenv("ELECTION_TICKS"); s != "" {
		if n, err := strconv.Atoi(s); err != nil || n < MinElectionTicks || n > MaxElectionTicks {
			errs = append(errs, fmt.Errorf("ELECTION_TICKS inválida %q: inteiro de %d a %d", s, MinElectionTicks, MaxElectionTicks))
		} else {
			c.ElectionTicks = n
		}
	}

	if s := getenv("NODE_ID"); s == "" {
		errs = append(errs, errors.New("variável obrigatória ausente: NODE_ID"))
	} else if id, err := strconv.ParseUint(s, 10, 64); err != nil || id == 0 {
		errs = append(errs, fmt.Errorf("NODE_ID inválido %q: inteiro maior que zero", s))
	} else {
		c.NodeID = id
	}
	var err error
	if c.Peers, err = parseMembers("PEERS", getenv("PEERS")); err != nil {
		errs = append(errs, err)
	} else if len(c.Peers) == 0 {
		errs = append(errs, errors.New("variável obrigatória ausente: PEERS"))
	} else if _, ok := c.Peers[c.NodeID]; c.NodeID != 0 && !ok {
		errs = append(errs, fmt.Errorf("PEERS não contém o NODE_ID %d", c.NodeID))
	}
	if t := getenv("TABLES"); t == "" {
		errs = append(errs, errors.New("variável obrigatória ausente: TABLES"))
	} else {
		for _, n := range strings.Split(t, ",") {
			n = strings.TrimSpace(n)
			if strings.Count(n, "{cell}") != 1 {
				errs = append(errs, fmt.Errorf("TABLES: a tabela %q precisa ter {cell} exatamente uma vez", n))
			}
			c.Tables = append(c.Tables, n)
		}
	}
	if c.AWSRegion == "" {
		errs = append(errs, errors.New("variável obrigatória ausente: AWS_REGION"))
	}

	if c.CellID == "" {
		errs = append(errs, errors.New("variável obrigatória ausente: CELL_ID"))
	}
	// Modo único strong (ADR-057): não há variável de modo. Um manifesto antigo que ainda
	// define uma delas falha na partida, em vez de ser ignorado.
	for _, k := range RemovedModeVars {
		if getenv(k) != "" {
			errs = append(errs, fmt.Errorf("%s não existe mais (modo único strong, ADR-057): retire-a da configuração", k))
		}
	}

	// Regras de consistência entre prazos: quem está mais dentro responde antes de quem está mais fora.
	if c.RequestDeadline >= c.ClientTimeout {
		errs = append(errs, errors.New("REQUEST_DEADLINE deve ser menor que CLIENT_TIMEOUT"))
	}
	if c.ForwardTimeout >= c.RequestDeadline {
		errs = append(errs, errors.New("FORWARD_TIMEOUT deve ser menor que REQUEST_DEADLINE"))
	}
	if c.WaitLeader > c.ForwardTimeout {
		errs = append(errs, errors.New("WAIT_LEADER deve caber em FORWARD_TIMEOUT (ADR-039)"))
	}
	if c.IdleTimeout <= MinIdleTimeout {
		errs = append(errs, fmt.Errorf("IDLE_TIMEOUT deve ser maior que %s (idle dos SDKs)", MinIdleTimeout))
	}
	return c, errors.Join(errs...)
}

// loadCompaction lê as variáveis da compactação do log (O-30, S.6 da trecho) e confere as
// regras de coerência entre elas. A regra que depende do tamanho do volume
// (RETAIN_MAX_BYTES < volume × RETAIN_DISK_HARD) é conferida na partida, pelo bootstrap.
func (c *Config) loadCompaction(getenv func(string) string, errs *[]error) {
	add := func(err error) { *errs = append(*errs, err) }
	size := func(key string, def, lo, hi int64) int64 {
		s := getenv(key)
		if s == "" {
			return def
		}
		n, err := domain.ParseSize(s)
		if err != nil || n < lo || n > hi {
			add(fmt.Errorf("%s inválida %q: tamanho de %s a %s (ex.: 64MiB)", key, s, fmtSize(lo), fmtSize(hi)))
			return def
		}
		return n
	}
	count := func(key string, def, lo, hi uint64) uint64 {
		s := getenv(key)
		if s == "" {
			return def
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || n < lo || n > hi {
			add(fmt.Errorf("%s inválida %q: inteiro de %d a %d", key, s, lo, hi))
			return def
		}
		return n
	}
	dur := func(key string, def, lo, hi time.Duration) time.Duration {
		s := getenv(key)
		if s == "" {
			return def
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < lo || d > hi {
			add(fmt.Errorf("%s inválida %q: duração de %s a %s", key, s, lo, hi))
			return def
		}
		return d
	}

	switch c.ClusterID = getenv("CLUSTER_ID"); {
	case c.ClusterID == "":
		add(errors.New("variável obrigatória ausente: CLUSTER_ID"))
	case !clusterIDRe.MatchString(c.ClusterID):
		add(fmt.Errorf("CLUSTER_ID inválida %q: de 1 a 64 caracteres [a-z0-9-]", c.ClusterID))
	}
	c.SnapshotEveryEntries = count("SNAPSHOT_EVERY_ENTRIES", 10_000, 100, 10_000_000)
	c.SnapshotEveryBytes = size("SNAPSHOT_EVERY_BYTES", 64*mib, mib, gib)
	c.RaftCatchupEntries = count("RAFT_CATCHUP_ENTRIES", 5_000, 0, 10_000_000)
	c.RaftRAMMax = size("RAFT_RAM_MAX", 256*mib, mib, 64*gib)
	c.RaftCompactInterval = dur("RAFT_COMPACT_INTERVAL", time.Second, 100*time.Millisecond, time.Minute)
	c.RetainMaxBytes = size("RETAIN_MAX_BYTES", 2*gib, mib, tib)
	c.RetainDiskHard = 0.85
	if s := getenv("RETAIN_DISK_HARD"); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err != nil || f < 0.5 || f > 0.95 {
			add(fmt.Errorf("RETAIN_DISK_HARD inválida %q: fração de 0.5 a 0.95", s))
		} else {
			c.RetainDiskHard = f
		}
	}
	c.RetainDiskHardBytes = size("RETAIN_DISK_HARD_BYTES", 0, 0, 64*tib)
	c.WalSegmentBytes = size("WAL_SEGMENT_BYTES", 64_000_000, 64*kib, gib)
	c.RebuildRetry = dur("REBUILD_RETRY", time.Minute, time.Second, time.Hour)
	c.RebuildPinTTL = dur("REBUILD_PIN_TTL", 5*time.Minute, 10*time.Second, time.Hour)
	c.PeerQueueBytes = size("PEER_QUEUE_BYTES", 64*mib, mib, gib)

	if c.RaftCatchupEntries >= c.SnapshotEveryEntries {
		add(errors.New("RAFT_CATCHUP_ENTRIES deve ser menor que SNAPSHOT_EVERY_ENTRIES"))
	}
	if c.RaftRAMMax <= c.SnapshotEveryBytes+uncommittedMax {
		add(errors.New("RAFT_RAM_MAX deve ser maior que SNAPSHOT_EVERY_BYTES + 32MiB (entradas não commitadas)"))
	}
	if c.RetainDiskHardBytes != 0 && c.RetainDiskHardBytes <= c.RetainMaxBytes {
		add(errors.New("RETAIN_DISK_HARD_BYTES deve ser maior que RETAIN_MAX_BYTES"))
	}
	if c.RetainMaxBytes < c.WalSegmentBytes {
		add(errors.New("RETAIN_MAX_BYTES deve ser maior ou igual a WAL_SEGMENT_BYTES"))
	}
}

// fmtSize escreve um tamanho no sufixo binário mais curto que o representa exatamente.
func fmtSize(n int64) string {
	for _, u := range []struct {
		s string
		m int64
	}{{"TiB", tib}, {"GiB", gib}, {"MiB", mib}, {"KiB", kib}} {
		if n >= u.m && n%u.m == 0 {
			return strconv.FormatInt(n/u.m, 10) + u.s
		}
	}
	return strconv.FormatInt(n, 10)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// parseMembers lê "id=host:porta,id=host:porta". Vazio devolve mapa vazio.
func parseMembers(key, s string) (map[uint64]string, error) {
	out := map[uint64]string{}
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
	for _, part := range strings.Split(s, ",") {
		idStr, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if !ok || err != nil || id == 0 || addr == "" {
			return nil, fmt.Errorf("%s inválida %q: use id=host:porta separados por vírgula", key, part)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("%s com o id %d repetido", key, id)
		}
		out[id] = addr
	}
	return out, nil
}

// IDs devolve os ids de um mapa de membros, em ordem.
func IDs(m map[uint64]string) []uint64 {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids
}

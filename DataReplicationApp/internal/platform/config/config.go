package config

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config é lido uma vez na partida e é imutável (12 Factors, III). Sem hot reload.
type Config struct {
	CellID    string // env CELL_ID (obrigatória)
	NodeID    uint64 // env NODE_ID (obrigatória, > 0; na POC igual ao CELL_ID)
	APIAddr   string // env API_ADDR
	PeerAddr  string // env PEER_ADDR
	AdminAddr string // env ADMIN_ADDR
	RaftDir   string // env RAFT_DIR: subdiretório do volume (C-04)
	ReplogDir string // env REPLOG_DIR

	// Membros do shard: id → endereço gRPC. Iguais em todas as células (RT-03).
	Peers    map[uint64]string // env PEERS: votantes
	Learners map[uint64]string // env LEARNERS: vazia = POC sem Azure (G-08)
	// POCNoAzure (env POC_SEM_AZURE=true) é a única forma de aceitar LEARNERS vazia.
	// SÓ DA POC (SG-12): em produção LEARNERS vazia derruba a partida.
	POCNoAzure bool

	Tables         []string // env TABLES: nomes lógicos com {cell}
	DynamoEndpoint string   // env DYNAMODB_ENDPOINT: vazio = AWS real
	AWSRegion      string   // env AWS_REGION

	TableWait           time.Duration // TABLE_WAIT: espera pelas tabelas na partida; padrão 120s
	GatePublishInterval time.Duration // GATE_PUBLISH_INTERVAL: padrão 100ms (G-06)
	ReplogSyncInterval  time.Duration // REPLOG_SYNC_INTERVAL: padrão 1s (AP-10)

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

	// Modo degradado (ADR-044 e ADR-045). DEGRADED_POLICY é OBRIGATÓRIA e sem valor
	// padrão. DEGRADED_LEASE (D) também não tem padrão e é obrigatória só com
	// DEGRADED_POLICY=accept; com reject é ignorada, sem erro (nunca há lease). O valor de
	// D depende do negócio (PEND-02).
	DegradedPolicy string        // env DEGRADED_POLICY: accept | reject
	DegradedLease  time.Duration // env DEGRADED_LEASE: validade D, contada desde t_c; 0 com reject

	// Orçamento de tempo (ver 9.4 do documento base e ADR-028). Opcionais, com padrão.
	ClientTimeout   time.Duration // CLIENT_TIMEOUT: menor timeout do SDK dos clientes; padrão 30s
	RequestDeadline time.Duration // REQUEST_DEADLINE: prazo de TODA requisição; padrão ClientTimeout/4 (7,5 s)
	DegradeAfter    time.Duration // DEGRADE_AFTER: espera pela Azure antes de propor DEGRADE; padrão 2s
	WaitLeader      time.Duration // WAIT_LEADER: espera por eleição antes do 503 (ADR-039); padrão 1s
	ForwardTimeout  time.Duration // FORWARD_TIMEOUT: limite do encaminhamento à líder; padrão 3s
	IdleTimeout     time.Duration // IDLE_TIMEOUT: conexão ociosa; padrão 120s (> idle dos SDKs)
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
		DegradedPolicy: getenv("DEGRADED_POLICY"),
		APIAddr:        orDefault(getenv("API_ADDR"), ":8000"),
		PeerAddr:       orDefault(getenv("PEER_ADDR"), ":7000"),
		AdminAddr:      orDefault(getenv("ADMIN_ADDR"), ":9090"),
		RaftDir:        orDefault(getenv("RAFT_DIR"), "/var/lib/dr/raft/wal"),
		ReplogDir:      orDefault(getenv("REPLOG_DIR"), "/var/lib/dr/replog"),
		DynamoEndpoint: getenv("DYNAMODB_ENDPOINT"),
		AWSRegion:      getenv("AWS_REGION"),
		POCNoAzure:     getenv("POC_SEM_AZURE") == "true",
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
	if c.DegradedPolicy == "accept" { // com reject (ou política inválida) a variável é ignorada
		c.DegradedLease = dur("DEGRADED_LEASE", 0, true) // sem padrão
	}
	c.ClientTimeout = dur("CLIENT_TIMEOUT", 30*time.Second, false)
	c.RequestDeadline = dur("REQUEST_DEADLINE", c.ClientTimeout/4, false)
	c.DegradeAfter = dur("DEGRADE_AFTER", 2*time.Second, false)
	c.WaitLeader = dur("WAIT_LEADER", time.Second, false)
	c.ForwardTimeout = dur("FORWARD_TIMEOUT", 3*time.Second, false)
	c.IdleTimeout = dur("IDLE_TIMEOUT", 120*time.Second, false)
	c.TableWait = dur("TABLE_WAIT", 120*time.Second, false)
	c.GatePublishInterval = dur("GATE_PUBLISH_INTERVAL", 100*time.Millisecond, false)
	c.ReplogSyncInterval = dur("REPLOG_SYNC_INTERVAL", time.Second, false)

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
	if c.Learners, err = parseMembers("LEARNERS", getenv("LEARNERS")); err != nil {
		errs = append(errs, err)
	}
	switch {
	case len(c.Learners) == 0 && !c.POCNoAzure:
		errs = append(errs, errors.New("LEARNERS vazia só é aceita na POC (POC_SEM_AZURE=true)"))
	case len(c.Learners) != 0 && len(c.Learners) != 3:
		errs = append(errs, fmt.Errorf("LEARNERS precisa ter 3 learners, veio %d", len(c.Learners)))
	}
	for id := range c.Learners {
		if _, dup := c.Peers[id]; dup {
			errs = append(errs, fmt.Errorf("o id %d está em PEERS e em LEARNERS", id))
		}
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

	for k, v := range map[string]string{"CELL_ID": c.CellID, "DEGRADED_POLICY": c.DegradedPolicy} {
		if v == "" {
			errs = append(errs, errors.New("variável obrigatória ausente: "+k))
		}
	}
	if c.DegradedPolicy != "" && c.DegradedPolicy != "accept" && c.DegradedPolicy != "reject" {
		errs = append(errs, fmt.Errorf("DEGRADED_POLICY inválida %q: use accept ou reject", c.DegradedPolicy))
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
	if c.DegradeAfter >= c.RequestDeadline {
		errs = append(errs, errors.New("DEGRADE_AFTER deve ser menor que REQUEST_DEADLINE"))
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
	// D é o teto do RPO > 0; um D menor que uma requisição não deixaria nem um ACK degradado.
	if c.DegradedPolicy == "accept" && c.DegradedLease != 0 && c.DegradedLease <= c.RequestDeadline {
		errs = append(errs, errors.New("DEGRADED_LEASE deve ser maior que REQUEST_DEADLINE"))
	}
	return c, errors.Join(errs...)
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

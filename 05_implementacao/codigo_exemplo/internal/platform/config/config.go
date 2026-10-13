package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Config é lido uma vez na partida e é imutável (12 Factors, III). Sem hot reload.
type Config struct {
	CellID          string // env CELL_ID (obrigatória)
	ReadModeDefault string // env READ_MODE_DEFAULT: strong | eventual (obrigatória; só tabela base, ADR-050)
	APIAddr         string
	AdminAddr       string
	WALDir          string

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

// MinIdleTimeout é o maior idle de SDK conhecido (Go, 90 s). O servidor precisa ficar
// acima, senão o SDK reusa uma conexão que o servidor já fechou.
const MinIdleTimeout = 90 * time.Second

// Load lê o ambiente do processo.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom lê de qualquer fonte (testes). Reúne TODOS os erros: o processo não sobe se
// faltar ou estiver incoerente qualquer valor.
func LoadFrom(getenv func(string) string) (Config, error) {
	var errs []error
	c := Config{
		CellID:          getenv("CELL_ID"),
		ReadModeDefault: getenv("READ_MODE_DEFAULT"),
		DegradedPolicy:  getenv("DEGRADED_POLICY"),
		APIAddr:         ":8000",
		AdminAddr:       ":9090",
		WALDir:          "/var/lib/dr/wal",
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

	for k, v := range map[string]string{"CELL_ID": c.CellID, "READ_MODE_DEFAULT": c.ReadModeDefault, "DEGRADED_POLICY": c.DegradedPolicy} {
		if v == "" {
			errs = append(errs, errors.New("variável obrigatória ausente: "+k))
		}
	}
	if c.ReadModeDefault != "" && c.ReadModeDefault != "strong" && c.ReadModeDefault != "eventual" {
		errs = append(errs, fmt.Errorf("READ_MODE_DEFAULT inválida %q: use strong ou eventual", c.ReadModeDefault))
	}
	if c.DegradedPolicy != "" && c.DegradedPolicy != "accept" && c.DegradedPolicy != "reject" {
		errs = append(errs, fmt.Errorf("DEGRADED_POLICY inválida %q: use accept ou reject", c.DegradedPolicy))
	}
	// WRITE_MODE saiu da v1 (ADR-053): o apply é sempre serial e depois do gate. Um
	// manifesto antigo que ainda a define falha na partida, em vez de ser ignorado.
	if getenv("WRITE_MODE") != "" {
		errs = append(errs, errors.New("WRITE_MODE foi removida da v1 (ADR-053): retire-a da configuração"))
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

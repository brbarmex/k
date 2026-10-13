package config

import (
	"errors"
	"os"
	"time"
)

// Config é lido uma vez na partida e é imutável (12 Factors, III). Sem hot reload.
type Config struct {
	CellID          string // env CELL_ID (obrigatória)
	WriteMode       string // env WRITE_MODE: strong | eventual (obrigatória)
	ReadModeDefault string // env READ_MODE_DEFAULT: strong | eventual (obrigatória)
	APIAddr         string
	AdminAddr       string
	WALDir          string

	// Orçamento de tempo (ver 9.4 do documento base).
	ClientTimeout   time.Duration // menor timeout do SDK dos clientes; padrão 30s
	RequestDeadline time.Duration // prazo de TODA requisição; padrão ClientTimeout/4
	DegradeAfter    time.Duration // espera pela Azure antes de propor DEGRADE; padrão 2s
}

func Load() (Config, error) {
	c := Config{
		CellID:          os.Getenv("CELL_ID"),
		WriteMode:       os.Getenv("WRITE_MODE"),
		ReadModeDefault: os.Getenv("READ_MODE_DEFAULT"),
		APIAddr:         ":8000",
		AdminAddr:       ":9090",
		WALDir:          "/var/lib/dr/wal",
		ClientTimeout:   30 * time.Second,
		DegradeAfter:    2 * time.Second,
	}
	c.RequestDeadline = c.ClientTimeout / 4
	var errs []error
	for k, v := range map[string]string{"CELL_ID": c.CellID, "WRITE_MODE": c.WriteMode, "READ_MODE_DEFAULT": c.ReadModeDefault} {
		if v == "" {
			errs = append(errs, errors.New("variável obrigatória ausente: "+k))
		}
	}
	// Regras de consistência entre prazos: quem está mais dentro responde antes de quem está mais fora.
	if c.RequestDeadline >= c.ClientTimeout {
		errs = append(errs, errors.New("REQUEST_DEADLINE deve ser menor que CLIENT_TIMEOUT"))
	}
	if c.DegradeAfter >= c.RequestDeadline {
		errs = append(errs, errors.New("DEGRADE_AFTER deve ser menor que REQUEST_DEADLINE"))
	}
	return c, errors.Join(errs...) // o processo não sobe se faltar algo
}

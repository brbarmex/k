package logx

import (
	"os"

	"github.com/rs/zerolog"
)

// New devolve um logger JSON para stdout (12 Factors, XI). Nada de log no caminho quente.
func New() zerolog.Logger {
	return zerolog.New(os.Stdout).With().Timestamp().Str("app", "datareplication").Logger()
}

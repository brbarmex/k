package peerv1

import (
	"errors"

	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// Códigos de resultado no fio. O Forward e a barreira preservam a CLASSE do resultado da
// líder, para a follower responder exatamente como a líder (RF-10).
const (
	CodeOK          = "OK"
	CodeNoLeader    = "NO_LEADER"
	CodeNotLeader   = "NOT_LEADER"
	CodePending     = "PENDING"
	CodeThrottling  = "THROTTLING"
	CodeReadTimeout = "READ_TIMEOUT"
	CodeInternal    = "INTERNAL"

	// Canal de recuperação (RT-19) e leitura fora de normal (AP-16).
	CodeNotRetained    = "NOT_RETAINED"
	CodeSourceNotReady = "SOURCE_NOT_READY"
	CodeRebuildAborted = "REBUILD_ABORTED"
	CodeCellRecovering = "CELL_RECOVERING"
)

// ErrInternal é o erro reconstruído de um INTERNAL vindo da líder.
var ErrInternal = errors.New("erro interno na líder")

// CodeOf traduz um erro do caso de uso no código do fio. nil vira OK.
func CodeOf(err error) string {
	switch {
	case err == nil:
		return CodeOK
	case errors.Is(err, usecase.ErrNoLeader):
		return CodeNoLeader
	case errors.Is(err, usecase.ErrNotLeader):
		return CodeNotLeader
	case errors.Is(err, usecase.ErrPending):
		return CodePending
	case errors.Is(err, usecase.ErrBackpressure):
		return CodeThrottling
	case errors.Is(err, usecase.ErrReadTimeout):
		return CodeReadTimeout
	case errors.Is(err, port.ErrNotRetained):
		return CodeNotRetained
	case errors.Is(err, port.ErrSourceNotReady):
		return CodeSourceNotReady
	case errors.Is(err, usecase.ErrRebuildAborted):
		return CodeRebuildAborted
	case errors.Is(err, usecase.ErrCellRecovering):
		return CodeCellRecovering
	default:
		return CodeInternal
	}
}

// ErrOf reconstrói o erro do caso de uso a partir do código do fio. OK vira nil.
func ErrOf(code string) error {
	switch code {
	case CodeOK:
		return nil
	case CodeNoLeader:
		return usecase.ErrNoLeader
	case CodeNotLeader:
		return usecase.ErrNotLeader
	case CodePending:
		return usecase.ErrPending
	case CodeThrottling:
		return usecase.ErrBackpressure
	case CodeReadTimeout:
		return usecase.ErrReadTimeout
	case CodeNotRetained:
		return port.ErrNotRetained
	case CodeSourceNotReady:
		return port.ErrSourceNotReady
	case CodeRebuildAborted:
		return usecase.ErrRebuildAborted
	case CodeCellRecovering:
		return usecase.ErrCellRecovering
	default:
		return ErrInternal
	}
}

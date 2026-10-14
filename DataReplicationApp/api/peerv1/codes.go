package peerv1

import (
	"errors"

	"example.com/datareplication/internal/usecase"
)

// Códigos de resultado no fio. O Forward e a barreira preservam a CLASSE do resultado da
// líder, para a follower responder exatamente como a líder (RF-10).
const (
	CodeOK           = "OK"
	CodeNoLeader     = "NO_LEADER"
	CodeNotLeader    = "NOT_LEADER"
	CodePending      = "PENDING"
	CodeThrottling   = "THROTTLING"
	CodeLeaseExpired = "LEASE_EXPIRED"
	CodeGateNotReady = "GATE_NOT_READY"
	CodeReadTimeout  = "READ_TIMEOUT"
	CodeInternal     = "INTERNAL"
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
	case errors.Is(err, usecase.ErrLeaseExpired):
		return CodeLeaseExpired
	case errors.Is(err, usecase.ErrGateNotReady):
		return CodeGateNotReady
	case errors.Is(err, usecase.ErrReadTimeout):
		return CodeReadTimeout
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
	case CodeLeaseExpired:
		return usecase.ErrLeaseExpired
	case CodeGateNotReady:
		return usecase.ErrGateNotReady
	case CodeReadTimeout:
		return usecase.ErrReadTimeout
	default:
		return ErrInternal
	}
}

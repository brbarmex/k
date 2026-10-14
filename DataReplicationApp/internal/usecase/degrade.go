package usecase

import (
	"context"
	"errors"
	"sync"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// ErrDegradeRefused: DEGRADED_POLICY=reject. A instalação nunca entra em modo degradado
// (ADR-045); sem o gate da Azure o cliente recebe 503 PENDING e o RPO é 0.
var ErrDegradeRefused = errors.New("DEGRADED_POLICY=reject: o shard nunca entra em modo degradado")

// Degrader é o que o WriteService precisa saber do modo degradado do shard.
type Degrader interface {
	// Active diz se o shard está em modo degradado (alimenta o header x-dr-mode).
	Active() bool
	// Enter propõe o DEGRADE com lease. Idempotente. Com reject devolve ErrDegradeRefused.
	Enter(ctx context.Context) error
	// CheckLease confere o lease (now < t_c + D); chamada a cada ACK (ADR-044, item 2).
	CheckLease() error
}

// DegradeService é o dono do estado degradado do shard: política, lease e o interruptor
// do gate. Entrar, restaurar e prorrogar passam sempre pelo Raft (entradas replicadas).
type DegradeService struct {
	Policy        domain.DegradedPolicy
	LeaseDuration time.Duration // D (DEGRADED_LEASE); valor depende do negócio (PEND-02)
	Consensus     port.ConsensusEngine
	Gate          *domain.Gate
	Lease         *domain.Lease // também recebe t_c do CommitPump
	Clock         domain.Clock  // injetável; produção usa time.Now (monotônico)

	// mu serializa Enter/Adopt/Restore/Renew. Fica preso durante o Propose, que é limitado
	// pelo prazo do chamador; os demais esperam no máximo esse prazo.
	mu sync.Mutex
}

var _ Degrader = (*DegradeService)(nil)

func (d *DegradeService) Active() bool { return d.Lease.Active() }

func (d *DegradeService) CheckLease() error { return d.Lease.Check(d.Clock()) }

// Enter propõe DEGRADE(epoch, D). O DEGRADE só MARCA o modo: o prazo do lease conta
// desde t_c, o último contato com a maioria Azure (alimentado pelo CommitPump), não
// desde esta proposta. Se a proposta ocorre muito depois de t_c, o lease já nasce
// vencido e as escritas recebem LEASE_EXPIRED. A ordem importa: só depois do commit do
// DEGRADE o lease é marcado e só então o gate deixa de contar a Azure, para que o gate
// nunca fique degradado sem o modo marcado.
func (d *DegradeService) Enter(ctx context.Context) error {
	if d.Policy != domain.PolicyAccept { // fecha para qualquer valor desconhecido
		return ErrDegradeRefused
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Lease.Active() {
		return nil
	}
	epoch := d.Lease.Epoch() + 1
	if _, err := d.Consensus.Propose(ctx, domain.Entry{Kind: domain.KindDegrade, Epoch: epoch, Lease: d.LeaseDuration}); err != nil {
		return err // não commitou: o shard continua normal (lado seguro)
	}
	d.Lease.Mark(epoch, d.LeaseDuration)
	d.Gate.SetDegraded(true)
	return nil
}

// Adopt é chamado por uma líder nova (ou por um nó reiniciado) ao reencontrar entradas
// de modo no log. Um DEGRADE em vigor é herdado SEM t_c: o lease fica vencido até haver
// contato com a Azure ou um RENEW (ADR-044, item 2). Um RENEW antigo não é herdado: a
// prorrogação só vale na líder que a fez, a partir do relógio dela.
func (d *DegradeService) Adopt(e domain.Entry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch e.Kind {
	case domain.KindDegrade:
		d.Lease.Inherit(e.Epoch, e.Lease)
		d.Gate.SetDegraded(true)
	case domain.KindRestore:
		d.Gate.SetDegraded(false)
		d.Lease.Clear()
	}
}

// Restore volta ao modo normal (RESTORE) depois que a Azure alcançou o log.
func (d *DegradeService) Restore(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.Lease.Active() {
		return nil
	}
	if _, err := d.Consensus.Propose(ctx, domain.Entry{Kind: domain.KindRestore, Epoch: d.Lease.Epoch()}); err != nil {
		return err
	}
	d.Gate.SetDegraded(false)
	d.Lease.Clear()
	return nil
}

// Renew é a prorrogação humana (RENEW), válida por dur a partir de agora mesmo sem
// contato com a Azure. Não há prorrogação automática. A interação
// com a promoção da Azure (H-DR13) é regra do modelo TLA+, não deste esboço.
func (d *DegradeService) Renew(ctx context.Context, dur time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.Lease.Active() {
		return errors.New("sem lease ativo para prorrogar")
	}
	epoch := d.Lease.Epoch()
	renewedAt := d.Clock() // lido antes da proposta, para errar do lado seguro
	if _, err := d.Consensus.Propose(ctx, domain.Entry{Kind: domain.KindRenew, Epoch: epoch, Lease: dur}); err != nil {
		return err
	}
	d.Lease.Renew(epoch, renewedAt, dur)
	return nil
}

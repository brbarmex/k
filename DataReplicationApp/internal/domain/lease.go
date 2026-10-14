package domain

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Clock é o relógio injetável. Em produção é time.Now, cuja leitura carrega o relógio
// MONOTÔNICO do Go: Sub entre duas leituras não sofre saltos do relógio de parede.
type Clock func() time.Time

// ErrLeaseExpired: o lease do modo degradado acabou (ADR-044); a API responde
// 503 LEASE_EXPIRED. Leitura e Raft seguem; só uma pessoa prorroga (RENEW).
var ErrLeaseExpired = errors.New("lease do modo degradado expirou")

// DegradedPolicy é a política de degradação da instalação (ADR-045).
type DegradedPolicy string

const (
	PolicyAccept DegradedPolicy = "accept" // modo degradado com lease e header
	PolicyReject DegradedPolicy = "reject" // nunca DEGRADE: sem gate da Azure, 503 PENDING
)

func ParseDegradedPolicy(s string) (DegradedPolicy, error) {
	switch p := DegradedPolicy(s); p {
	case PolicyAccept, PolicyReject:
		return p, nil
	}
	return "", fmt.Errorf("DEGRADED_POLICY inválida %q: use accept ou reject", s)
}

// Lease é a validade D do modo degradado (ADR-044).
//
// O prazo NÃO conta desde a proposta do DEGRADE: conta desde o ÚLTIMO CONTATO com a
// maioria dos learners da Azure, t_c, o instante de ENVIO (relógio monotônico da líder)
// do último heartbeat ou AppendEntries que 2 de 3 learners confirmaram. A conferência
// a cada ACK é now < t_c + D. A entrada DEGRADE só marca o modo. Assim a janela não
// depende de quando chega a primeira escrita depois da partição: proposta tardia com
// t_c antigo já nasce vencida.
//
// Líder nova (ou nó reiniciado) que herda o DEGRADE não herda t_c: o lease fica vencido
// até haver contato com a maioria Azure no termo novo ou um RENEW (Inherit).
//
// Zero value = modo normal (sem lease ativo). Seguro para uso concorrente.
type Lease struct {
	mu       sync.Mutex
	active   bool // DEGRADE em vigor
	epoch    uint64
	duration time.Duration // D

	term     Term
	contacts map[string]time.Time // t_c por learner, SÓ no termo atual

	renewedAt time.Time // RENEW humano: vale até renewedAt + renewDur
	renewDur  time.Duration
}

// SetTerm informa o termo atual da líder. Termo maior descarta os contatos (líder nova
// não herda t_c) e o RENEW anterior.
func (l *Lease) SetTerm(t Term) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t > l.term {
		l.term = t
		l.contacts = nil
		l.renewDur = 0
	}
}

// ObserveContact registra que o learner confirmou uma mensagem ENVIADA em sentAt (lida
// no relógio monotônico da líder antes do envio). Só vale no termo atual e nunca recua.
func (l *Lease) ObserveContact(term Term, learner string, sentAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if term != l.term || learner == "" || sentAt.IsZero() {
		return
	}
	if old, ok := l.contacts[learner]; ok && !sentAt.After(old) {
		return
	}
	if _, ok := l.contacts[learner]; !ok && len(l.contacts) >= MaxLearners {
		return
	}
	if l.contacts == nil {
		l.contacts = make(map[string]time.Time, MaxLearners)
	}
	l.contacts[learner] = sentAt
}

// contactLocked devolve t_c: o 2º instante mais recente entre os learners (maioria de 3).
func (l *Lease) contactLocked() (time.Time, bool) {
	if len(l.contacts) < 2 {
		return time.Time{}, false
	}
	ts := make([]time.Time, 0, len(l.contacts))
	for _, t := range l.contacts {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(a, b int) bool { return ts[a].After(ts[b]) })
	return ts[1], true
}

// Mark marca o modo degradado da época epoch com duração D (entrada DEGRADE commitada).
// Não zera os contatos: o prazo segue contando desde t_c.
func (l *Lease) Mark(epoch uint64, d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active, l.epoch, l.duration = true, epoch, d
}

// Inherit é o que uma líder nova (ou um nó reiniciado) faz ao encontrar um DEGRADE em
// vigor no log: marca o modo, mas sem t_c e sem RENEW, ou seja, com o lease vencido até
// haver contato com a maioria Azure ou um RENEW.
func (l *Lease) Inherit(epoch uint64, d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active, l.epoch, l.duration = true, epoch, d
	l.contacts = nil
	l.renewDur = 0
}

// Renew é a prorrogação humana (entrada RENEW replicada): vale por dur a partir de
// renewedAt (relógio lido antes da proposta), mesmo sem contato com a Azure. A regra
// contra a promoção da Azure (H-DR13) é do modelo TLA+.
func (l *Lease) Renew(epoch uint64, renewedAt time.Time, dur time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.active || l.epoch != epoch {
		return false
	}
	l.renewedAt, l.renewDur = renewedAt, dur
	return true
}

// Clear encerra o modo degradado (RESTORE).
func (l *Lease) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active = false
	l.renewDur = 0
}

func (l *Lease) Active() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

func (l *Lease) Epoch() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch
}

// Check devolve nil fora do modo degradado ou com lease válido (now < t_c + D, ou dentro
// de um RENEW), e ErrLeaseExpired caso contrário, inclusive sem nenhum t_c. Um relógio
// que anda para trás (now antes do instante de referência) conta como expirado.
func (l *Lease) Check(now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.active {
		return nil
	}
	if l.renewDur > 0 {
		if e := now.Sub(l.renewedAt); e >= 0 && e < l.renewDur {
			return nil
		}
	}
	tc, ok := l.contactLocked()
	if !ok {
		return ErrLeaseExpired
	}
	if e := now.Sub(tc); e < 0 || e >= l.duration {
		return ErrLeaseExpired
	}
	return nil
}

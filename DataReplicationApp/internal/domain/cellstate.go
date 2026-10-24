package domain

import "sync"

// CellState é o estado da célula diante do log (AP-16).
type CellState uint8

const (
	// CellNormal: a célula acompanha o log pelo Raft.
	CellNormal CellState = iota
	// CellRecovering: a célula está atrás da cauda e alcança pelo canal de recuperação (AP-17).
	CellRecovering
	// CellRebuilding: a célula está fora da janela e copia o banco de uma célula sã (AP-18).
	CellRebuilding
)

func (s CellState) String() string {
	switch s {
	case CellRecovering:
		return "recuperando"
	case CellRebuilding:
		return "reconstruindo"
	default:
		return "normal"
	}
}

// CellMode guarda o estado da célula, o alvo da recuperação e a fonte escolhida.
// Todos os métodos são seguros para uso concorrente; o valor zero é a célula em normal.
type CellMode struct {
	mu      sync.Mutex
	state   CellState
	target  Index
	source  string
	changed chan struct{}
}

func (m *CellMode) State() CellState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *CellMode) Target() Index {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.target
}

func (m *CellMode) Source() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.source
}

func (m *CellMode) SetSource(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.source = s
}

// EnterRecovering põe a célula em recuperação até pelo menos t. Em reconstrução não muda
// nada (a reconstrução já termina adiante do marcador) e devolve false. A subida do alvo
// também avisa quem espera em Changed: o CommitPump parado no alvo antigo volta a pedir.
func (m *CellMode) EnterRecovering(t Index) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == CellRebuilding {
		return false
	}
	prev, prevTarget := m.state, m.target
	m.state, m.target = CellRecovering, max(m.target, t)
	if prev != m.state || m.target != prevTarget {
		m.notifyLocked()
	}
	return true
}

// EnterRebuilding vale de qualquer estado: zera o alvo e a fonte.
func (m *CellMode) EnterRebuilding() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.state
	m.state, m.target, m.source = CellRebuilding, 0, ""
	if prev != m.state {
		m.notifyLocked()
	}
	return true
}

// SetNormal só vale de CellRecovering. De CellRebuilding, só a partida define o estado
// (a reconstrução termina com reinício, DC-04).
func (m *CellMode) SetNormal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != CellRecovering {
		return
	}
	m.state, m.target, m.source = CellNormal, 0, ""
	m.notifyLocked()
}

// SetNormalIf volta a normal só se a célula ainda está em recuperação com o alvo t: um alvo
// que subiu enquanto o aplicado alcançava t mantém a recuperação. Devolve se voltou.
func (m *CellMode) SetNormalIf(t Index) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != CellRecovering || m.target != t {
		return false
	}
	m.state, m.target, m.source = CellNormal, 0, ""
	m.notifyLocked()
	return true
}

// Changed devolve um canal fechado na próxima transição de estado (e na subida do alvo).
func (m *CellMode) Changed() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.changed == nil {
		m.changed = make(chan struct{})
	}
	return m.changed
}

func (m *CellMode) notifyLocked() {
	if m.changed != nil {
		close(m.changed)
		m.changed = nil
	}
}

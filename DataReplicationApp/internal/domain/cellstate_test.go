package domain

import (
	"sync"
	"testing"
)

// O-31, AP-16: as transições do CellMode.
func TestCellModeTransitions(t *testing.T) {
	var m CellMode
	if m.State() != CellNormal || m.State().String() != "normal" {
		t.Fatalf("estado inicial = %v", m.State())
	}
	ch := m.Changed()
	if !m.EnterRecovering(10) || m.State() != CellRecovering || m.Target() != 10 {
		t.Fatalf("recuperando: %v alvo %d", m.State(), m.Target())
	}
	select {
	case <-ch:
	default:
		t.Fatal("Changed não fechou na transição")
	}
	m.EnterRecovering(5) // o alvo só sobe
	if m.Target() != 10 {
		t.Fatalf("alvo recuou para %d", m.Target())
	}
	m.EnterRecovering(20)
	m.SetSource("2")
	if m.Target() != 20 || m.Source() != "2" {
		t.Fatalf("alvo %d fonte %q", m.Target(), m.Source())
	}
	m.SetNormal()
	if m.State() != CellNormal || m.Target() != 0 || m.Source() != "" {
		t.Fatalf("normal: %v alvo %d fonte %q", m.State(), m.Target(), m.Source())
	}
	m.EnterRecovering(30)
	m.SetSource("3")
	if !m.EnterRebuilding() || m.State() != CellRebuilding || m.Target() != 0 || m.Source() != "" {
		t.Fatalf("reconstruindo: %v alvo %d fonte %q", m.State(), m.Target(), m.Source())
	}
	if m.State().String() != "reconstruindo" || CellRecovering.String() != "recuperando" {
		t.Fatal("nomes dos estados")
	}
	if m.EnterRecovering(40) || m.State() != CellRebuilding {
		t.Fatal("em reconstrução, EnterRecovering não muda nada")
	}
	m.SetNormal() // só a partida tira de reconstruindo (DC-04)
	if m.State() != CellRebuilding {
		t.Fatal("SetNormal não vale de reconstruindo")
	}
}

// N-06: o CellMode é seguro para uso concorrente.
func TestCellModeConcurrent(t *testing.T) {
	var m CellMode
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ch := m.Changed()
			m.EnterRecovering(Index(i))
			_ = m.State()
			m.SetNormal()
			select {
			case <-ch:
			default:
			}
		}(i)
	}
	wg.Wait()
}

// O-39: a subida do alvo avisa quem espera, e SetNormalIf só volta a normal no alvo certo.
func TestCellModeTargetRiseAndSetNormalIf(t *testing.T) {
	var m CellMode
	m.EnterRecovering(10)
	ch := m.Changed()
	m.EnterRecovering(10) // mesmo alvo: nada muda
	select {
	case <-ch:
		t.Fatal("alvo igual não devia avisar")
	default:
	}
	m.EnterRecovering(15)
	select {
	case <-ch:
	default:
		t.Fatal("a subida do alvo devia avisar")
	}
	if m.SetNormalIf(10) || m.State() != CellRecovering {
		t.Fatal("alvo antigo não volta a normal")
	}
	if !m.SetNormalIf(15) || m.State() != CellNormal {
		t.Fatal("o alvo atual volta a normal")
	}
	m.EnterRebuilding()
	if m.SetNormalIf(0) {
		t.Fatal("de reconstruindo, só a partida")
	}
}

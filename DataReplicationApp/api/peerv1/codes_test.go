package peerv1

import (
	"errors"
	"testing"

	"example.com/datareplication/internal/port"
	"example.com/datareplication/internal/usecase"
)

// N-03: cada código tem o par CodeOf/ErrOf (os do canal de recuperação incluídos, RT-19).
func TestCodesSymmetric(t *testing.T) {
	for _, err := range []error{usecase.ErrNoLeader, usecase.ErrNotLeader, usecase.ErrPending, usecase.ErrBackpressure,
		usecase.ErrReadTimeout, port.ErrNotRetained, port.ErrSourceNotReady, usecase.ErrRebuildAborted,
		usecase.ErrCellRecovering} {
		code := CodeOf(err)
		if code == CodeInternal {
			t.Fatalf("%v sem código próprio", err)
		}
		if back := ErrOf(code); !errors.Is(back, err) {
			t.Fatalf("%v → %s → %v", err, code, back)
		}
	}
	if CodeOf(nil) != CodeOK || ErrOf(CodeOK) != nil {
		t.Fatal("OK")
	}
}

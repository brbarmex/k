package domain

import (
	"context"
	"testing"
	"time"
)

func TestGate(t *testing.T) {
	g := &Gate{}
	g.SetRaftCommit(10)
	g.SetAzureDurable([3]Index{10, 7, 3})
	if g.Index() != 7 {
		t.Fatalf("esperado 7, veio %d", g.Index())
	}
	g.SetAzureDurable([3]Index{5, 5, 5}) // não recua
	if g.Index() != 7 {
		t.Fatal("gate recuou")
	}
	ctx, c := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer c()
	if g.WaitFor(ctx, 9) == nil {
		t.Fatal("não devia liberar")
	}
	g.SetDegraded(true)
	if g.WaitFor(context.Background(), 10) != nil {
		t.Fatal("degradado devia liberar até o commit Raft")
	}
}

package memstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

func newStore(t *testing.T, latency time.Duration) *Store {
	t.Helper()
	tm, err := domain.NewTableMap("1", []string{"t_{cell}"})
	if err != nil {
		t.Fatal(err)
	}
	return New(tm, latency)
}

func put(key string, item string, v domain.Index) port.VersionedOp {
	return port.VersionedOp{Op: domain.Op{Table: "t_{cell}", Kind: domain.OpPut, Key: []byte(key), Item: []byte(item)}, Version: v}
}

// A versão manda: só versão maior sobrescreve, e versão igual ou menor não é erro (SG-06).
func TestApplyIsConditionalOnVersion(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, 0)
	if err := s.ApplyBatch(ctx, []port.VersionedOp{put(`{"pk":{"S":"a"}}`, `{"v":{"S":"5"}}`, 5)}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBatch(ctx, []port.VersionedOp{put(`{"pk":{"S":"a"}}`, `{"v":{"S":"3"}}`, 3)}); err != nil {
		t.Fatalf("versão antiga devia ser ignorada sem erro: %v", err)
	}
	got, _ := s.Get(ctx, "t_{cell}", []byte(`{"pk":{"S":"a"}}`))
	if string(got) != `{"v":{"S":"5"}}` {
		t.Fatalf("Get = %s; a versão 3 não podia sobrescrever a 5", got)
	}
	if got, _ := s.Get(ctx, "t_{cell}", []byte(`{"pk":{"S":"x"}}`)); got != nil {
		t.Fatalf("chave ausente devia dar nil, veio %s", got)
	}
}

func TestUnknownTableAndDeleteArePermanent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, 0)
	op := put(`{"pk":{"S":"a"}}`, `{}`, 1)
	op.Table = "outra_{cell}"
	if err := s.ApplyBatch(ctx, []port.VersionedOp{op}); !errors.Is(err, port.ErrStorePermanent) {
		t.Fatalf("tabela fora da lista devia ser permanente, veio %v", err)
	}
	del := put(`{"pk":{"S":"a"}}`, ``, 1)
	del.Kind = domain.OpDelete
	if err := s.ApplyBatch(ctx, []port.VersionedOp{del}); !errors.Is(err, port.ErrStorePermanent) {
		t.Fatalf("delete devia ser permanente nesta fase, veio %v", err)
	}
}

func TestScanPagesInKeyOrder(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, 0)
	for i, k := range []string{"c", "a", "b"} {
		if err := s.ApplyBatch(ctx, []port.VersionedOp{put(`{"pk":{"S":"`+k+`"}}`, `{"pk":{"S":"`+k+`"}}`, domain.Index(i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	p1, err := s.Scan(ctx, "t_{cell}", 2, nil)
	if err != nil || len(p1.Items) != 2 || string(p1.LastKey) != `{"pk":{"S":"b"}}` {
		t.Fatalf("primeira página: %+v, %v", p1, err)
	}
	p2, _ := s.Scan(ctx, "t_{cell}", 2, p1.LastKey)
	if len(p2.Items) != 1 || p2.LastKey != nil || string(p2.Items[0]) != `{"pk":{"S":"c"}}` {
		t.Fatalf("segunda página: %+v", p2)
	}
}

// A latência simulada respeita o prazo: cancelado, o erro é transitório (o applier repete).
func TestLatencyHonorsContext(t *testing.T) {
	s := newStore(t, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.ApplyBatch(ctx, []port.VersionedOp{put(`{"pk":{"S":"a"}}`, `{}`, 1)}); !errors.Is(err, port.ErrStoreTransient) {
		t.Fatalf("esperava erro transitório, veio %v", err)
	}
}

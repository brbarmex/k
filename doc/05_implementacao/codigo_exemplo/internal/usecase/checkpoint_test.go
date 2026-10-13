package usecase

import (
	"context"
	"testing"

	"example.com/datareplication/internal/domain"
)

type ckLog struct {
	fakeLog
	copied, durable, compactedTo domain.Index
}

func (l *ckLog) Sync(context.Context) (domain.Index, error) {
	l.durable = l.copied
	return l.durable, nil
}
func (l *ckLog) DurableIndex() domain.Index { return l.durable }
func (l *ckLog) CompactPrefix(_ context.Context, upTo domain.Index) error {
	l.compactedTo = upTo
	return nil
}

type fakeRaft struct {
	minMatch, compactedTo domain.Index
}

func (r *fakeRaft) MinPeerMatch() domain.Index      { return r.minMatch }
func (r *fakeRaft) Compact(upTo domain.Index) error { r.compactedTo = upTo; return nil }

func TestCheckpointCompactsRaftOnlyToDurable(t *testing.T) {
	log := &ckLog{}
	raft := &fakeRaft{minMatch: 1000}
	c := &Checkpointer{Log: log, Raft: raft, RetainMax: 100}

	log.copied = 50 // copiado, ainda sem fsync
	if raft.compactedTo != 0 {
		t.Fatal("compactou antes do checkpoint")
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if raft.compactedTo != 50 {
		t.Fatalf("Raft compactado até %d, quer 50 (o durável do checkpoint)", raft.compactedTo)
	}
}

func TestCheckpointTruncatesReplogByAzureAndApplied(t *testing.T) {
	log := &ckLog{copied: 100}
	raft := &fakeRaft{minMatch: 100}
	c := &Checkpointer{Log: log, Raft: raft, RetainMax: 100,
		Retention: func() (domain.Index, domain.Index, bool, domain.Index) { return 80, 60, true, 0 }}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if log.compactedTo != 60 {
		t.Fatalf("log de replicação truncado até %d, quer 60 (maioria Azure)", log.compactedTo)
	}
}

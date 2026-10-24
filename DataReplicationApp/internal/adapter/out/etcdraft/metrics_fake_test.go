package etcdraft

import (
	"strconv"
	"sync"
	"time"
)

// recMetrics registra as chamadas de port.Metrics (testes da O-16).
type recMetrics struct {
	mu    sync.Mutex
	calls map[string]int
}

func (r *recMetrics) inc(k string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	r.calls[k]++
}

func (r *recMetrics) n(k string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[k]
}

func (r *recMetrics) LeaderChanged(uint64)                  { r.inc("leader") }
func (r *recMetrics) RaftMessageSent(t string)              { r.inc("sent:" + t) }
func (r *recMetrics) RaftMessageReceived(t string)          { r.inc("recv:" + t) }
func (r *recMetrics) HeartbeatSeen()                        { r.inc("heartbeat") }
func (r *recMetrics) WALFsync(time.Duration)                { r.inc("fsync") }
func (r *recMetrics) PeerSendDropped(uint64)                { r.inc("dropped") }
func (r *recMetrics) Forwarded()                            { r.inc("forwarded") }
func (r *recMetrics) WriteDone(res string, _ time.Duration) { r.inc("write:" + res) }
func (r *recMetrics) ReadDone(op, res string)               { r.inc("read:" + op + ":" + res) }
func (r *recMetrics) ReadBarrier(time.Duration)             { r.inc("barrier") }
func (r *recMetrics) ApplyItem(time.Duration)               { r.inc("apply") }
func (r *recMetrics) RaftLoop(time.Duration)                { r.inc("raftloop") }
func (r *recMetrics) RaftTickGap(time.Duration)             { r.inc("tickgap") }
func (r *recMetrics) Throttled(c string)                    { r.inc("throttled:" + c) }
func (r *recMetrics) HTTPRequest(op string, st int, _ time.Duration) {
	r.inc("http:" + op + ":" + strconv.Itoa(st))
}
func (r *recMetrics) HTTPInFlight(d int)            { r.inc("inflight:" + strconv.Itoa(d)) }
func (r *recMetrics) SnapshotTaken(time.Duration)   { r.inc("snapshot") }
func (r *recMetrics) SnapshotDeferred()             { r.inc("snapshot_deferred") }
func (r *recMetrics) StartupDuration(time.Duration) { r.inc("startup") }
func (r *recMetrics) ReplicationStage(etapa string, _ time.Duration) {
	r.inc("repl_" + etapa)
}
func (r *recMetrics) RecoveryFinished(uint64, time.Duration) { r.inc("recovery") }
func (r *recMetrics) RAMCeilingHit()                         { r.inc("ram_ceiling") }

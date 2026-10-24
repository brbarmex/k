package etcdraft

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"go.etcd.io/etcd/server/v3/storage/wal/walpb"
	"go.etcd.io/raft/v3"
	"google.golang.org/protobuf/proto"

	"example.com/datareplication/internal/domain"
	"example.com/datareplication/internal/port"
)

// CompactionConfig liga a compactação do log (RT-11, RT-14, RT-15). Progress nulo
// desliga o Compactor (testes do consenso sem applier).
type CompactionConfig struct {
	Progress port.ApplyProgress // fsync do progresso e aplicado durável (AP-06, SG-18)
	Gate     *domain.Gate       // confirmado gravado no marcador (G6)

	EveryEntries   uint64        // SNAPSHOT_EVERY_ENTRIES
	EveryBytes     int64         // SNAPSHOT_EVERY_BYTES
	CatchupEntries uint64        // RAFT_CATCHUP_ENTRIES
	RAMMax         int64         // RAFT_RAM_MAX
	RetainMax      int64         // RETAIN_MAX_BYTES
	HardFrac       float64       // RETAIN_DISK_HARD (fração do volume)
	HardBytes      int64         // RETAIN_DISK_HARD_BYTES (0 = desligado)
	Interval       time.Duration // RAFT_COMPACT_INTERVAL
}

// CompactorStatus é o retrato do último ciclo, para o /status.
type CompactorStatus struct {
	Floor         domain.Index
	RetainedBytes int64
	RetainedFiles int
	Pins          int
	HardCeiling   bool
	HeldByApplied bool
	Window        map[uint64]bool
	MemByPeer     map[uint64]int64 // bytes na RAM desde o Match de cada par (só na líder, DC-14)
}

// Compactor é o ciclo snapshot → RAM → disco, numa goroutine própria, fora do laço do
// Ready (SG-22). É o único que chama wal.SaveSnapshot, MemoryStorage.CreateSnapshot,
// MemoryStorage.Compact, wal.ReleaseLockTo e a remoção de arquivos. Nunca segura uma trava
// do Node durante o fsync: o WAL do etcd serializa SaveSnapshot e Save com o próprio
// mutex, então o laço espera no máximo um fsync extra.
type Compactor struct {
	node    *Node
	cfg     CompactionConfig
	ret     *Retention
	volume  string // diretório do volume do Raft (statfs do teto duro)
	metrics port.Metrics
	log     zerolog.Logger
	warn    *rateLog

	lastAppended int64 // bytes acrescentados à RAM no momento do último snapshot

	// usage mede a fração usada do volume (syscall.Statfs); trocada nos testes.
	usage func(path string) (float64, error)

	mu sync.Mutex
	st CompactorStatus
}

func newCompactor(n *Node, cfg CompactionConfig, ret *Retention) *Compactor {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	return &Compactor{
		node: n, cfg: cfg, ret: ret, volume: filepath.Dir(n.cfg.Dir), metrics: n.metrics, log: n.log,
		warn: newRateLog(10 * time.Second), usage: volumeUsage,
	}
}

// Run roda um ciclo a cada intervalo. Um erro de fsync (progresso, marcador, WAL) é
// devolvido e derruba o processo (RT-05).
func (c *Compactor) Run(ctx context.Context) error {
	t := time.NewTicker(c.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			if err := c.cycle(now); err != nil {
				return err
			}
		}
	}
}

// cycle é um ciclo completo: snapshot (se o gatilho disparou), RAM e disco.
func (c *Compactor) cycle(now time.Time) error {
	advanced := c.node.Advanced()
	snap := c.node.SnapshotIndex()
	_, ramBytes := c.node.ram.stats()
	appended := c.node.ram.appended.Load()
	due := advanced > snap && (uint64(advanced-snap) >= c.cfg.EveryEntries || appended-c.lastAppended >= c.cfg.EveryBytes)
	// Teto da RAM: compactar até o snapshot exige um snapshot no índice avançado antes.
	overRAM := c.cfg.RAMMax > 0 && ramBytes > c.cfg.RAMMax
	if due || (overRAM && advanced > snap) {
		if err := c.snapshot(advanced, appended); err != nil {
			return err
		}
	}
	segs, err := c.ret.Segments()
	if err != nil {
		c.log.Warn().Err(err).Msg("compactação: não deu para listar o WAL")
		return nil
	}
	floor, window := c.floor(segs)
	c.compactRAM(now, floor)
	return c.releaseDisk(now, segs, floor, window)
}

// snapshot grava o snapshot-marcador em S, na ordem do RT-11: progresso com fsync,
// arquivo do marcador, wal.SaveSnapshot, CreateSnapshot. Uma queda entre os passos só
// deixa um marcador mais antigo como ponto de partida.
func (c *Compactor) snapshot(s domain.Index, appended int64) error {
	t0 := time.Now()
	durable, err := c.cfg.Progress.Sync()
	if err != nil {
		return fmt.Errorf("etcdraft: fsync do progresso antes do snapshot: %w", err)
	}
	var confirmed domain.Index
	if c.cfg.Gate != nil {
		confirmed = c.cfg.Gate.Index()
	}
	m := domain.SnapshotMarker{Version: domain.MarkerVersion, ClusterID: c.node.cfg.ClusterID, Epoch: 1, Confirmed: confirmed}
	if err := writeMarker(c.node.cfg.MarkerPath, m, s); err != nil {
		return err
	}
	term, err := c.node.storage.Term(uint64(s))
	if err != nil {
		return fmt.Errorf("etcdraft: termo do snapshot %d: %w", s, err)
	}
	cs := c.node.confState()
	if err := c.node.wal.SaveSnapshot(&walpb.Snapshot{Index: proto.Uint64(uint64(s)), Term: proto.Uint64(term), ConfState: cs}); err != nil {
		return fmt.Errorf("etcdraft: wal.SaveSnapshot: %w", err)
	}
	if _, err := c.node.storage.CreateSnapshot(uint64(s), cs, m.Encode()); err != nil && !errors.Is(err, raft.ErrSnapOutOfDate) {
		return fmt.Errorf("etcdraft: CreateSnapshot: %w", err)
	}
	c.node.setSnapshot(s, m)
	c.lastAppended = appended
	d := time.Since(t0)
	c.metrics.SnapshotTaken(d)
	c.log.Info().Uint64("indice", uint64(s)).Uint64("aplicadoDuravel", uint64(durable)).
		Uint64("confirmado", uint64(confirmed)).Dur("duracao", d).Msg("snapshot gravado")
	return nil
}

// floor calcula o piso de retenção: na líder, o menor Match das réplicas na janela
// (DC-08), publicado às followers pelo GateUpdate; na follower, o último recebido.
func (c *Compactor) floor(segs []domain.Segment) (domain.Index, map[uint64]bool) {
	if !c.node.isLeader() {
		return c.ret.Floor(), nil
	}
	v := domain.RetentionView{Segments: segs, Snapshot: c.node.SnapshotIndex()}
	floor, window := domain.LeaderFloor(c.node.matches(), c.node.cfg.ID, v, c.cfg.RetainMax)
	c.ret.SetFloor(floor)
	return floor, window
}

// compactRAM compacta o MemoryStorage até snapshot − RAFT_CATCHUP_ENTRIES (RT-14). Na
// líder, não passa do menor Match das réplicas na janela (DC-08). No teto (RAFT_RAM_MAX),
// compacta até o snapshot, ignorando o Match (SG-23). Nunca passa do snapshot: o Raft da
// líder precisa do snapshot no MemoryStorage para enviar a quem ficou atrás dele.
func (c *Compactor) compactRAM(now time.Time, floor domain.Index) {
	snap := c.node.SnapshotIndex()
	var to domain.Index
	if uint64(snap) > c.cfg.CatchupEntries {
		to = snap - domain.Index(c.cfg.CatchupEntries)
	}
	if c.node.isLeader() {
		if floor == 0 {
			to = 0 // uma réplica na janela com Match 0 segura toda a RAM (até o teto)
		} else {
			to = min(to, floor-1)
		}
	}
	if _, ramBytes := c.node.ram.stats(); c.cfg.RAMMax > 0 && ramBytes > c.cfg.RAMMax {
		to = snap
		c.metrics.RAMCeilingHit()
		if ok, n := c.warn.allow("teto-ram", now); ok {
			c.log.Warn().Int64("bytes", ramBytes).Int64("teto", c.cfg.RAMMax).Uint64("snapshot", uint64(snap)).
				Int("suprimidas", n).Msg("memória do Raft no teto: compactando até o snapshot")
		}
	}
	first := c.node.ramFirst()
	if to+1 <= first {
		return
	}
	if err := c.node.storage.Compact(uint64(to)); err != nil {
		if !errors.Is(err, raft.ErrCompacted) {
			c.log.Warn().Err(err).Uint64("ate", uint64(to)).Msg("compactação da RAM falhou")
		}
		return
	}
	c.node.ram.recordCompact(to)
}

// releaseDisk libera os segmentos .wal até limit = min(aplicado durável, piso, pinos,
// snapshot) (RT-15, SG-18). O progresso tem fsync antes (AP-06). No teto duro, solta só
// os pinos de reconstrução; se o aplicado local é o que segura, nada dele é apagado e a
// célula se declara fora (/readyz falso).
func (c *Compactor) releaseDisk(now time.Time, segs []domain.Segment, floor domain.Index, window map[uint64]bool) error {
	durable, err := c.cfg.Progress.Sync()
	if err != nil {
		return fmt.Errorf("etcdraft: fsync do progresso antes de liberar o disco: %w", err)
	}
	view := func() domain.RetentionView {
		return domain.RetentionView{Segments: segs, AppliedDurable: durable, Floor: floor,
			MinPin: c.ret.MinPin(now), Snapshot: c.node.SnapshotIndex()}
	}
	limit := c.limit(view())
	total := view().RetainedSince(0)
	hard := c.cfg.HardBytes > 0 && total >= c.cfg.HardBytes
	if !hard && c.cfg.HardFrac > 0 {
		if used, err := c.usage(c.volume); err == nil && used >= c.cfg.HardFrac {
			hard = true
		}
	}
	if hard && c.ret.DropRebuildPins() {
		limit = c.limit(view())
		c.log.Warn().Int64("retido", total).Msg("teto duro de disco: reconstrução abortada")
	}
	heldByApplied := hard && limit == durable
	if hard {
		if ok, n := c.warn.allow("teto-disco", now); ok {
			c.log.Warn().Int64("retido", total).Uint64("limite", uint64(limit)).Bool("seguradoPeloAplicado", heldByApplied).
				Int("suprimidas", n).Msg("disco do log no teto duro")
		}
	}
	if limit > 0 {
		if err := c.node.wal.ReleaseLockTo(uint64(limit)); err != nil {
			c.log.Warn().Err(err).Msg("wal.ReleaseLockTo falhou")
		}
		// Só os arquivos já destravados pelo ReleaseLockTo: o seguinte começa antes do limite.
		for _, s := range view().Removable(limit - 1) {
			if err := os.Remove(filepath.Join(c.ret.dir, segmentName(s.Seq, uint64(s.First)))); err != nil && !errors.Is(err, os.ErrNotExist) {
				c.log.Warn().Err(err).Uint64("seq", s.Seq).Msg("remoção de segmento do WAL falhou; tento no próximo ciclo")
				continue
			}
			c.log.Info().Uint64("seq", s.Seq).Uint64("primeiro", uint64(s.First)).Uint64("limite", uint64(limit)).
				Uint64("piso", uint64(floor)).Uint64("aplicadoDuravel", uint64(durable)).Msg("segmento do WAL removido")
		}
	}
	if after, err := c.ret.Segments(); err == nil {
		segs = after
	}
	v := view()
	c.mu.Lock()
	c.st = CompactorStatus{Floor: floor, RetainedBytes: v.RetainedSince(0), RetainedFiles: len(segs), Pins: c.ret.Pins(),
		HardCeiling: hard, HeldByApplied: heldByApplied, Window: window, MemByPeer: c.memByPeer()}
	c.mu.Unlock()
	return nil
}

// memByPeer calcula, na líder, os bytes da RAM desde o Match de cada par (DC-14). Uma vez
// por ciclo: o scrape só lê o resultado.
func (c *Compactor) memByPeer() map[uint64]int64 {
	if !c.node.isLeader() {
		return nil
	}
	out := map[uint64]int64{}
	for id, m := range c.node.matches() {
		if id != c.node.cfg.ID {
			out[id] = c.node.ram.bytesAfter(m)
		}
	}
	return out
}

// limit é o limite da view, com um pino em 0 segurando tudo.
func (c *Compactor) limit(v domain.RetentionView) domain.Index {
	if c.ret.hasPinAtZero() {
		return 0
	}
	return v.Limit()
}

// Status devolve o retrato do último ciclo.
func (c *Compactor) Status() CompactorStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st
	if st.Window != nil {
		w := make(map[uint64]bool, len(st.Window))
		for k, v := range st.Window {
			w[k] = v
		}
		st.Window = w
	}
	if st.MemByPeer != nil {
		m := make(map[uint64]int64, len(st.MemByPeer))
		for k, v := range st.MemByPeer {
			m[k] = v
		}
		st.MemByPeer = m
	}
	return st
}

// volumeUsage devolve a fração usada do sistema de arquivos de path (1 − livres/total).
func volumeUsage(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	if st.Blocks == 0 {
		return 0, nil
	}
	return 1 - float64(st.Bavail)/float64(st.Blocks), nil
}

// VolumeBytes devolve o tamanho total do sistema de arquivos de path, para a conferência
// de partida RETAIN_MAX_BYTES < volume × RETAIN_DISK_HARD (O-37).
func VolumeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Blocks) * int64(st.Bsize), nil
}

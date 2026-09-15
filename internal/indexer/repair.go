package indexer

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/user/revimg/internal/db"
	"github.com/user/revimg/internal/hasher"
)

// repairBatchSize bounds each finder query so cycles stream instead of
// loading the whole archive into memory.
const repairBatchSize = 50

// yieldWait is how long the repair worker sleeps while scans are active.
const yieldWait = 30 * time.Second

// RepairProgress is a live snapshot of the background health worker.
type RepairProgress struct {
	Running     bool      `json:"running"`
	Total       int64     `json:"total"` // work items estimated at cycle start
	Checked     int64     `json:"checked"`
	Repaired    int64     `json:"repaired"`
	Errors      int64     `json:"errors"`
	SHABytes    int64     `json:"sha_bytes"`
	Current     string    `json:"current,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// RepairStatus returns a copy of the current (or last) repair progress,
// or nil if no cycle has ever run.
func (idx *Indexer) RepairStatus() *RepairProgress {
	idx.repairMu.Lock()
	defer idx.repairMu.Unlock()
	if idx.repairProg == nil {
		return nil
	}
	cp := *idx.repairProg
	return &cp
}

// StartRepair begins a health cycle in the background. It returns false
// when repair is disabled or a cycle is already running. Manual starts are
// allowed during scans — the worker yields to them on its own.
func (idx *Indexer) StartRepair() bool {
	if !idx.cfg.RepairEnabled {
		return false
	}
	idx.repairMu.Lock()
	if idx.repairProg != nil && idx.repairProg.Running {
		idx.repairMu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(idx.ctx)
	idx.repairCancel = cancel
	prog := &RepairProgress{Running: true, StartedAt: time.Now()}
	idx.repairProg = prog
	idx.repairWg.Add(1)
	idx.repairMu.Unlock()
	go idx.runRepairCycle(ctx, prog)
	return true
}

// CancelRepair stops a running cycle. Reports whether one was running.
func (idx *Indexer) CancelRepair() bool {
	idx.repairMu.Lock()
	defer idx.repairMu.Unlock()
	if idx.repairProg == nil || !idx.repairProg.Running {
		return false
	}
	if idx.repairCancel != nil {
		idx.repairCancel()
	}
	return true
}

// maybeStartRepair starts a cycle only when the system is otherwise idle.
// Called after scans complete and by the interval ticker.
func (idx *Indexer) maybeStartRepair() {
	if !idx.cfg.RepairEnabled || idx.anyScanActive() {
		return
	}
	idx.StartRepair()
}

// repairLoop runs the interval ticker. The post-boot pass is covered by the
// ScanDir idle hook; the ticker covers everything after, plus a delayed
// first check in case no scan ever runs.
func (idx *Indexer) repairLoop() {
	select {
	case <-idx.ctx.Done():
		return
	case <-time.After(2 * time.Minute):
		idx.maybeStartRepair()
	}
	for {
		hours := idx.cfg.RepairIntervalH
		if hours <= 0 {
			hours = 24
		}
		select {
		case <-idx.ctx.Done():
			return
		case <-time.After(time.Duration(hours) * time.Hour):
			idx.maybeStartRepair()
		}
	}
}

func (idx *Indexer) anyScanActive() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, p := range idx.progress {
		if !p.Done {
			return true
		}
	}
	return false
}

// runRepairCycle drains missing fingerprints first, then trickles through
// the archive oldest-verified-first. Every step commits, so a crash or
// restart resumes where it left off with no cursor to maintain.
func (idx *Indexer) runRepairCycle(ctx context.Context, prog *RepairProgress) {
	lowerPriority()
	defer idx.repairWg.Done()
	// Whole-second timestamps: shift one second back so rows stamped in the
	// same second the cycle starts (by a just-finished scan, or by this
	// cycle's own repairs) are strictly newer and never re-fetched.
	// Without this, same-second rows loop forever within the cycle.
	cycleStart := prog.StartedAt.Unix() - 1
	log.Print("[repair] cycle started")
	defer func() {
		idx.repairMu.Lock()
		if idx.repairProg != nil && idx.repairProg.Running {
			idx.repairProg.Running = false
			idx.repairProg.Current = ""
			idx.repairProg.CompletedAt = time.Now()
		}
		idx.repairMu.Unlock()
		if ctx.Err() != nil {
			log.Print("[repair] cycle stopped")
		}
	}()

	missing, _ := idx.db.CountMissingFingerprints(ctx, cycleStart)
	unverified, _ := idx.db.CountUnverifiedSince(ctx, cycleStart)
	idx.repairMu.Lock()
	prog.Total = missing + unverified
	idx.repairMu.Unlock()
	idx.Events.Push(EventRepairStarted, "", "")

	// P0: videos stored without usable fingerprints.
	for {
		if !idx.repairIdle(ctx) {
			return
		}
		batch, err := idx.db.FilesMissingFingerprints(ctx, cycleStart, repairBatchSize)
		if err != nil || len(batch) == 0 {
			break
		}
		for _, p := range batch {
			if !idx.repairIdle(ctx) {
				return
			}
			idx.setRepairCurrent(prog, p)
			idx.repairReindex(ctx, prog, p)
			if !sleepCtx(ctx, time.Duration(idx.cfg.RepairDelayMs)*time.Millisecond) {
				return
			}
		}
	}

	// P0.5: old index errors get one retry per cycle.
	for {
		if !idx.repairIdle(ctx) {
			return
		}
		batch, err := idx.db.FilesWithOldErrors(ctx, cycleStart, repairBatchSize)
		if err != nil || len(batch) == 0 {
			break
		}
		for _, p := range batch {
			if !idx.repairIdle(ctx) {
				return
			}
			idx.setRepairCurrent(prog, p)
			idx.repairReindex(ctx, prog, p)
			if !sleepCtx(ctx, time.Duration(idx.cfg.RepairDelayMs)*time.Millisecond) {
				return
			}
		}
	}

	// P1/P2: oldest-first verification trickle.
	shaBudget := idx.cfg.RepairSHABudgetBytes
	var shaSpent int64
	for {
		if !idx.repairIdle(ctx) {
			return
		}
		batch, err := idx.db.NextVerifyBatch(ctx, cycleStart, repairBatchSize)
		if err != nil || len(batch) == 0 {
			break // cycle complete
		}
		for _, c := range batch {
			if !idx.repairIdle(ctx) {
				return
			}
			idx.setRepairCurrent(prog, c.Path)
			shaSpent = idx.verifyOne(ctx, prog, c, cycleStart, shaSpent, shaBudget)
			idx.bumpRepairChecked(prog)
			if !sleepCtx(ctx, time.Duration(idx.cfg.RepairDelayMs)*time.Millisecond) {
				return
			}
		}
	}

	now := time.Now().Unix()
	state, _ := idx.db.GetRepairState(ctx)
	state.LastCycleCompletedAt = now
	state.LastCycleChecked = prog.Checked
	state.LastCycleRepaired = prog.Repaired
	state.LastCycleErrors = prog.Errors
	state.LastCycleSHABytes = prog.SHABytes
	_ = idx.db.SetRepairState(ctx, state)

	idx.repairMu.Lock()
	prog.Running = false
	prog.Current = ""
	prog.CompletedAt = time.Now()
	idx.repairMu.Unlock()
	idx.Events.Push(EventRepairCompleted, "",
		"checked="+strconv.FormatInt(prog.Checked, 10)+
			" repaired="+strconv.FormatInt(prog.Repaired, 10))
	log.Printf("[repair] cycle complete checked=%d repaired=%d errors=%d",
		prog.Checked, prog.Repaired, prog.Errors)
}

// verifyOne checks a single healthy-candidate file, cheapest test first.
// Returns the updated SHA byte counter.
func (idx *Indexer) verifyOne(ctx context.Context, prog *RepairProgress, c db.VerifyCandidate, cycleStart, shaSpent, shaBudget int64) int64 {
	info, err := os.Stat(c.Path)
	now := time.Now().Unix()
	if err != nil {
		// Gone from disk: delete only when the surrounding mount looks
		// healthy, otherwise leave it for a later cycle (see watcher).
		if idx.watcher.mountOK(c.Path) {
			if parent, perr := os.Stat(filepath.Dir(c.Path)); perr == nil && parent.IsDir() {
				_ = idx.db.DeleteFileByPath(ctx, c.Path)
				idx.Events.Push(EventDeleted, c.Path, "repair: gone from disk")
				idx.bumpRepairRepaired(prog)
				return shaSpent
			}
		}
		return shaSpent // preserved; stays unverified for next cycle
	}
	if info.Size() != c.Size || info.ModTime().Unix() != c.ModifiedAt {
		idx.repairReindex(ctx, prog, c.Path) // upsert stamps verified
		return shaSpent
	}
	// Stat matches: spend SHA budget on bitrot sampling, oldest first.
	if c.SHA256 != "" && shaSpent+info.Size() <= shaBudget {
		if sum, serr := hasher.FileSHA256(c.Path); serr == nil {
			shaSpent += info.Size()
			if sum != c.SHA256 {
				idx.repairReindex(ctx, prog, c.Path)
				return shaSpent
			}
		} else {
			idx.bumpRepairErrors(prog)
		}
	}
	_ = idx.db.TouchVerified(ctx, c.ID, now)
	return shaSpent
}

// repairReindex re-indexes one path with throttled ffmpeg and records the
// outcome. The upsert stamps last_verified_at on success.
func (idx *Indexer) repairReindex(ctx context.Context, prog *RepairProgress, path string) {
	if ctx.Err() != nil {
		return
	}
	if err := idx.indexFileWithThreads(ctx, path, idx.cfg.RepairThreads); err != nil {
		log.Printf("[repair] %s: %v", path, err)
		idx.bumpRepairErrors(prog)
		idx.Events.Push(EventError, path, err.Error())
		return
	}
	// Refresh the verified stamp even when indexFile fast-pathed (it only
	// skips when already verified, and upsert stamps on write).
	if f, ferr := idx.db.GetFileByPath(ctx, path); ferr == nil && f != nil {
		_ = idx.db.TouchVerified(ctx, f.ID, time.Now().Unix())
	}
	idx.bumpRepairRepaired(prog)
}

// repairIdle yields to user-initiated scans: while any scan runs, sleep in
// 30s increments. Returns false when the cycle is cancelled (the deferred
// cleanup in runRepairCycle marks the progress stopped).
func (idx *Indexer) repairIdle(ctx context.Context) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		if !idx.anyScanActive() {
			return true
		}
		if !sleepCtx(ctx, yieldWait) {
			return false
		}
	}
}

func (idx *Indexer) setRepairCurrent(prog *RepairProgress, path string) {
	idx.repairMu.Lock()
	prog.Current = path
	idx.repairMu.Unlock()
}

func (idx *Indexer) bumpRepairChecked(prog *RepairProgress) {
	idx.repairMu.Lock()
	prog.Checked++
	idx.repairMu.Unlock()
}

func (idx *Indexer) bumpRepairRepaired(prog *RepairProgress) {
	idx.repairMu.Lock()
	prog.Repaired++
	idx.repairMu.Unlock()
}

func (idx *Indexer) bumpRepairErrors(prog *RepairProgress) {
	idx.repairMu.Lock()
	prog.Errors++
	idx.repairMu.Unlock()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Package indexer owns the pipeline that discovers files, extracts features,
// and writes them to the database.
package indexer

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/revimg/internal/config"
	"github.com/user/revimg/internal/db"
	"github.com/user/revimg/internal/hasher"
)

// SupportedImageExts is the set of image extensions the indexer handles.
var SupportedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".tiff": true, ".tif": true, ".webp": true,
}

// SupportedVideoExts lists container formats handled via ffmpeg.
var SupportedVideoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true,
	".webm": true, ".flv": true, ".wmv": true, ".m4v": true,
	".mpg": true, ".mpeg": true, ".ts": true, ".mts": true,
}

// Progress is a live snapshot of an ongoing directory scan.
type Progress struct {
	DirID     int64     `json:"dir_id"`
	JobID     int64     `json:"job_id"`
	Total     int       `json:"total"`
	Processed int32     `json:"processed"` // updated atomically
	Errors    int32     `json:"errors"`     // updated atomically
	Done      bool      `json:"done"`
	StartedAt time.Time `json:"started_at"`
}

// writeReq is a queued file upsert request from a worker to the single DB writer.
type writeReq struct {
	file  *db.File
	errCh chan error
}

// Indexer orchestrates file scanning, feature extraction and DB writes.
type Indexer struct {
	db       *db.DB
	cfg      *config.Config
	ffmpegOK bool

	mu         sync.RWMutex
	progress   map[int64]*Progress // keyed by dir ID
	writeQueue chan writeReq

	watcher  *Watcher
	ctx      context.Context
	cancel   context.CancelFunc
	writerWg sync.WaitGroup
	scansWg  sync.WaitGroup

	repairMu     sync.Mutex
	repairProg   *RepairProgress
	repairCancel context.CancelFunc
	repairWg     sync.WaitGroup

	Events *EventLog
}

// New creates an Indexer. Call Start() to begin watching.
func New(database *db.DB, cfg *config.Config) *Indexer {
	ctx, cancel := context.WithCancel(context.Background())
	idx := &Indexer{
		db:         database,
		cfg:        cfg,
		progress:   make(map[int64]*Progress),
		writeQueue: make(chan writeReq, 512),
		ctx:        ctx,
		cancel:     cancel,
	}
	idx.ffmpegOK = hasher.CheckFFmpeg(cfg.FFmpegPath, cfg.FFprobePath) == nil
	if !idx.ffmpegOK {
		log.Println("[indexer] ffmpeg/ffprobe not found — video indexing disabled")
	}
	idx.Events = NewEventLog(1000)
	idx.watcher = newWatcher(idx)
	idx.writerWg.Add(1)
	go idx.fileWriter()
	return idx
}

// fileWriter is a single goroutine that serializes all DB writes.
func (idx *Indexer) fileWriter() {
	defer idx.writerWg.Done()
	for {
		select {
		case req := <-idx.writeQueue:
			req.errCh <- idx.db.UpsertFile(context.Background(), req.file)
		case <-idx.ctx.Done():
			idx.drainWriteQueue()
			return
		}
	}
}

// drainWriteQueue processes any remaining items after shutdown is requested.
func (idx *Indexer) drainWriteQueue() {
	for {
		select {
		case req := <-idx.writeQueue:
			req.errCh <- idx.db.UpsertFile(context.Background(), req.file)
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

// Start begins the file-system watcher loop and re-scans enabled directories.
func (idx *Indexer) Start() {
	dirs, err := idx.db.WatchDirs(idx.ctx)
	if err != nil {
		log.Printf("[indexer] cannot load watch dirs: %v", err)
		return
	}
	for _, d := range dirs {
		if d.Enabled {
			idx.watcher.Watch(d.Path, d.Recursive)
			go idx.ScanDir(idx.ctx, d)
		}
	}
	go idx.repairLoop()
	idx.watcher.Run(idx.ctx)
}

// AddDir registers a new watch directory, persists it, and starts a scan.
func (idx *Indexer) AddDir(ctx context.Context, path string, recursive bool, extensions []string) (*db.WatchDir, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	d := &db.WatchDir{
		Path:       abs,
		Recursive:  recursive,
		Extensions: extensions,
		Enabled:    true,
	}
	id, err := idx.db.InsertWatchDir(ctx, d)
	if err != nil {
		return nil, err
	}
	d.ID = id
	idx.watcher.Watch(abs, d.Recursive)
	go idx.ScanDir(idx.ctx, d)
	return d, nil
}

// RemoveDir removes a watch directory and stops watching it.
func (idx *Indexer) RemoveDir(ctx context.Context, id int64) error {
	dirs, _ := idx.db.WatchDirs(ctx)
	for _, d := range dirs {
		if d.ID == id {
			idx.watcher.Unwatch(d.Path)
		}
	}
	return idx.db.DeleteWatchDir(ctx, id)
}

// Shutdown gracefully stops indexing. It cancels all scans, drains the write
// queue, and returns when complete or ctx is cancelled. Pending count logged
// every 2s so you're not staring at a blank terminal during a long drain.
func (idx *Indexer) Shutdown(ctx context.Context) error {
	log.Print("[indexer] shutting down…")
	idx.cancel()

	// Log pending count every 2s while we wait.
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				n := len(idx.writeQueue)
				log.Printf("[indexer] waiting for %d pending writes…", n)
			case <-ctx.Done():
				return
			}
		}
	}()

	idx.scansWg.Wait()

	// Repair worker shares idx.ctx, so it is already winding down; join it
	// before draining the writer it may still be queueing to.
	idx.repairWg.Wait()

	// Writer is already in drain mode (ctx cancelled). Wait for it.
	done := make(chan struct{})
	go func() {
		idx.writerWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Print("[indexer] write queue drained, shutdown complete")
		return nil
	case <-ctx.Done():
		n := len(idx.writeQueue)
		log.Printf("[indexer] shutdown timeout (%d writes pending)", n)
		return ctx.Err()
	}
}

// ScanDir scans a directory, indexes new/changed files, removes deleted ones.
func (idx *Indexer) ScanDir(ctx context.Context, d *db.WatchDir) {
	idx.scansWg.Add(1)
	defer idx.scansWg.Done()

	jobID, err := idx.db.CreateScanJob(ctx, d.ID)
	if err != nil {
		log.Printf("[indexer] create job: %v", err)
		return
	}

	prog := &Progress{
		DirID:     d.ID,
		JobID:     jobID,
		StartedAt: time.Now(),
	}
	idx.mu.Lock()
	idx.progress[d.ID] = prog
	idx.mu.Unlock()

	defer func() {
		idx.mu.Lock()
		prog.Done = true
		idx.mu.Unlock()
	}()

	log.Printf("[indexer] scanning %s", d.Path)

	// If the root directory has zero entries it's likely an unmounted drive.
	// Skip scanning entirely to avoid wiping the index.
	if ents, _ := os.ReadDir(d.Path); len(ents) == 0 {
		log.Printf("[indexer] warning: %s is empty (unmounted?) — skipping scan", d.Path)
		idx.db.UpdateScanJob(ctx, jobID, 0, 0, 0, "skipped")
		return
	}

	paths, err := walk(d.Path, d.Recursive, d.Extensions, idx.cfg.VideoEnabled && idx.ffmpegOK, idx.cfg.IgnoredDirs)
	if err != nil {
		log.Printf("[indexer] walk %s: %v", d.Path, err)
		idx.db.UpdateScanJob(ctx, jobID, 0, 0, 1, "failed")
		return
	}

	prog.Total = len(paths)
	idx.Events.Push(EventScanStarted, d.Path, fmt.Sprintf("%d files", prog.Total))
	idx.db.UpdateScanJob(ctx, jobID, prog.Total, 0, 0, "running")

	numWorkers := idx.cfg.NumWorkers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	jobs := make(chan string, numWorkers*4)
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := idx.indexFile(ctx, path); err != nil {
					log.Printf("[indexer] %s: %v", path, err)
					atomic.AddInt32(&prog.Errors, 1)
				}
				n := atomic.AddInt32(&prog.Processed, 1)
				if n%50 == 0 {
					idx.db.UpdateScanJob(ctx, jobID,
						prog.Total, int(n), int(atomic.LoadInt32(&prog.Errors)), "running")
				}
			}
		}()
	}

	for _, p := range paths {
		select {
		case jobs <- p:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()

	// Remove stale DB entries for files that no longer exist on disk.
	if ctx.Err() == nil {
		current := make(map[string]struct{}, len(paths))
		for _, p := range paths {
			current[p] = struct{}{}
		}
		stored, _ := idx.db.FilePathsUnder(ctx, d.Path)
		for _, p := range stored {
			if _, ok := current[p]; !ok {
				if err := idx.db.DeleteFileByPath(ctx, p); err == nil {
					log.Printf("[indexer] removed stale: %s", p)
				}
			}
		}
	}

	status := "completed"
	if ctx.Err() != nil {
		status = "failed"
	}
	total  := int(atomic.LoadInt32(&prog.Processed))
	errors := int(atomic.LoadInt32(&prog.Errors))
	idx.db.UpdateScanJob(ctx, jobID, prog.Total, total, errors, status)
	idx.db.UpdateWatchDirScan(ctx, d.ID, total)
	idx.Events.Push(EventScanCompleted, d.Path, fmt.Sprintf("processed=%d errors=%d", total, errors))

	log.Printf("[indexer] scan complete %s processed=%d errors=%d", d.Path, total, errors)

	// The system is idle again — let the health loop check whether
	// anything (missed or stale) needs reindexing.
	idx.maybeStartRepair()
}

// ReindexFile forces re-indexing of a single path (called by watcher on change).
func (idx *Indexer) ReindexFile(ctx context.Context, path string) {
	if err := idx.indexFile(ctx, path); err != nil {
		log.Printf("[indexer] reindex %s: %v", path, err)
	}
}

// RemoveFile removes a single file from the index.
func (idx *Indexer) RemoveFile(ctx context.Context, path string) {
	if err := idx.db.DeleteFileByPath(ctx, path); err != nil {
		log.Printf("[indexer] remove %s: %v", path, err)
	}
}

// GetProgress returns a copy of the current scan progress for a directory.
func (idx *Indexer) GetProgress(dirID int64) *Progress {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	p := idx.progress[dirID]
	if p == nil {
		return nil
	}
	cp := *p
	cp.Processed = atomic.LoadInt32(&p.Processed)
	cp.Errors    = atomic.LoadInt32(&p.Errors)
	return &cp
}

// AllProgress returns progress for all directories.
func (idx *Indexer) AllProgress() map[int64]*Progress {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make(map[int64]*Progress, len(idx.progress))
	for k, p := range idx.progress {
		cp := *p
		cp.Processed = atomic.LoadInt32(&p.Processed)
		cp.Errors    = atomic.LoadInt32(&p.Errors)
		out[k] = &cp
	}
	return out
}

// isIgnoredDir returns true if the directory name is in the ignored list.
func (idx *Indexer) isIgnoredDir(name string) bool {
	for _, d := range idx.cfg.IgnoredDirs {
		if d == name {
			return true
		}
	}
	return false
}

// queueWrite sends a file upsert to the single-writer goroutine and waits for the result.
func (idx *Indexer) queueWrite(ctx context.Context, f *db.File) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	ch := make(chan error, 1)
	select {
	case idx.writeQueue <- writeReq{f, ch}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// indexFile indexes one path with default (full-speed) settings.
func (idx *Indexer) indexFile(ctx context.Context, path string) error {
	return idx.indexFileWithThreads(ctx, path, 0)
}

// indexFileWithThreads is indexFile with an ffmpeg thread cap for
// background repair work (0 = default, all cores).
func (idx *Indexer) indexFileWithThreads(ctx context.Context, path string, threads int) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Size() == 0 || info.Size() > idx.cfg.MaxFileSize {
		return nil
	}

	// Fast path: skip if mtime unchanged and last index was clean.
	// PHash != 0 is required so rows poisoned by past toolchain failures
	// (fingerprinted with empty hashes but no error) get re-done on rescan.
	existing, _ := idx.db.GetFileByPath(ctx, path)
	if existing != nil && existing.ModifiedAt == info.ModTime().Unix() && existing.IndexError == "" && existing.PHash != 0 {
		return nil
	}

	isNew := existing == nil
	ext := strings.ToLower(filepath.Ext(path))
	rec := &db.File{
		Path:       path,
		Filename:   filepath.Base(path),
		Directory:  filepath.Dir(path),
		Extension:  ext,
		Size:       info.Size(),
		ModifiedAt: info.ModTime().Unix(),
		IndexedAt:  time.Now().Unix(),
		CreatedAt:  info.ModTime().Unix(), // birth time not portable; use mtime
	}

	// Content hash for change detection
	if sha, err := hasher.FileSHA256(path); err == nil {
		rec.SHA256 = sha
		// Content unchanged and previously errored → skip re-processing
		if existing != nil && existing.SHA256 == sha && existing.IndexError != "" {
			return nil
		}
		// Content unchanged → update timestamps only
		if existing != nil && existing.SHA256 == sha && existing.PHash != 0 {
			existing.ModifiedAt = rec.ModifiedAt
			existing.IndexedAt  = rec.IndexedAt
			if err := idx.queueWrite(ctx, existing); err != nil {
				return err
			}
			idx.Events.Push(EventModified, path, "timestamp update")
			return nil
		}
	}

	// Verify content MIME type matches extension — rejects text files with
	// ambiguous extensions (e.g. .ts = TypeScript source vs MPEG Transport Stream).
	if !isSupportedContent(path) {
		return nil
	}

	switch {
	case SupportedImageExts[ext]:
		rec.FileType = db.FileTypeImage
		if err := idx.extractImage(ctx, rec, path); err != nil {
			rec.IndexError = err.Error()
		}

	case SupportedVideoExts[ext] && idx.cfg.VideoEnabled && idx.ffmpegOK:
		rec.FileType = db.FileTypeVideo
		if err := idx.extractVideo(ctx, rec, path, threads); err != nil {
			rec.IndexError = err.Error()
		}

	default:
		return nil // unsupported
	}

	if err := idx.queueWrite(ctx, rec); err != nil {
		idx.Events.Push(EventError, path, err.Error())
		return err
	}
	if rec.IndexError == "" {
		if isNew {
			idx.Events.Push(EventCreated, path, "")
		} else {
			idx.Events.Push(EventModified, path, "")
		}
	}
	return nil
}

func (idx *Indexer) extractImage(ctx context.Context, rec *db.File, path string) error {
	feats, err := hasher.ExtractImageFeatures(path)
	if err != nil {
		return err
	}
	rec.PHash          = feats.PHash
	rec.AHash          = feats.AHash
	rec.DHash          = feats.DHash
	rec.Width          = feats.Width
	rec.Height         = feats.Height
	rec.ColorHistogram = hasher.SerializeHistogram(feats.ColorHistogram)
	rec.DominantColors = feats.DominantColors
	rec.ExifMake       = feats.ExifMake
	rec.ExifModel      = feats.ExifModel
	rec.ExifDatetime   = feats.ExifDatetime
	rec.ExifGPSLat     = feats.ExifGPSLat
	rec.ExifGPSLng     = feats.ExifGPSLng

	// Thumbnail generated after DB write (we need the ID)
	go idx.ensureImageThumb(path, rec.ID)
	return nil
}

func (idx *Indexer) extractVideo(ctx context.Context, rec *db.File, path string, threads int) error {
	feats, err := hasher.ExtractVideoFeatures(ctx, path, hasher.ProbeConfig{
		FFmpegPath:  idx.cfg.FFmpegPath,
		FFprobePath: idx.cfg.FFprobePath,
		SampleFPS:   idx.cfg.VideoFPS,
		MaxFrames:   idx.cfg.MaxFrames,
		Threads:     threads,
	})
	if err != nil {
		return err
	}
	rec.Width          = feats.Width
	rec.Height         = feats.Height
	rec.DurationMs     = feats.DurationMs
	rec.FPS            = feats.FPS
	rec.Codec          = feats.Codec
	rec.AudioCodec     = feats.AudioCodec
	rec.Bitrate        = feats.Bitrate
	rec.FrameCount     = feats.FrameCount
	rec.PHash          = feats.PHash
	rec.AHash          = feats.AHash
	rec.DHash          = feats.DHash
	rec.VideoPHashes   = feats.FrameHashes
	rec.ColorHistogram = hasher.SerializeHistogram(feats.ColorHistogram)

	go idx.ensureVideoThumb(path, rec.ID, feats.DurationMs)
	return nil
}

func (idx *Indexer) thumbPathByID(id int64) string {
	return filepath.Join(idx.cfg.ThumbDir(), fmt.Sprintf("%d.jpg", id))
}

func (idx *Indexer) ensureImageThumb(srcPath string, id int64) {
	dst := idx.thumbPathByID(id)
	if _, err := os.Stat(dst); err == nil {
		return
	}
	if err := hasher.GenerateThumbnail(srcPath, dst, idx.cfg.ThumbSize); err != nil {
		log.Printf("[indexer] thumb %s: %v", srcPath, err)
	}
}

func (idx *Indexer) ensureVideoThumb(srcPath string, id int64, durMs int64) {
	dst := idx.thumbPathByID(id)
	if _, err := os.Stat(dst); err == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := hasher.VideoThumbnail(ctx, idx.cfg.FFmpegPath, srcPath, dst, durMs, idx.cfg.ThumbSize); err != nil {
		log.Printf("[indexer] video thumb %s: %v", srcPath, err)
	}
}

// ─── Directory walker ─────────────────────────────────────────────────────────

// isSupportedContent reads the first 512 bytes to detect MIME type and
// rejects files whose content doesn't match an image or video format.
// This catches ambiguous extensions like .ts (TypeScript vs MPEG-TS).
func isSupportedContent(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	mime := http.DetectContentType(buf[:n])
	return strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "video/")
}

func walk(root string, recursive bool, filterExts []string, videoEnabled bool, ignoredDirs []string) ([]string, error) {
	filterSet := make(map[string]bool, len(filterExts))
	for _, e := range filterExts {
		filterSet[strings.ToLower(e)] = true
	}
	ignored := make(map[string]bool, len(ignoredDirs))
	for _, d := range ignoredDirs {
		ignored[d] = true
	}

	var paths []string
	seen := make(map[string]bool) // guard symlink cycles

	var walkDir func(dir string, depth int)
	walkDir = func(dir string, depth int) {
		if !recursive && depth > 1 {
			return
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}

		for _, e := range entries {
			name := e.Name()
			if ignored[name] {
				continue
			}
			full := filepath.Join(dir, name)
			if e.IsDir() {
				if recursive {
					walkDir(full, depth+1)
				}
				continue
			}

			ext := strings.ToLower(filepath.Ext(name))
			if len(filterSet) > 0 && !filterSet[ext] {
				continue
			}
			if !SupportedImageExts[ext] && !(videoEnabled && SupportedVideoExts[ext]) {
				continue
			}
			paths = append(paths, full)
		}
	}

	walkDir(root, 1)
	return paths, nil
}

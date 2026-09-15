package indexer

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceDelay = 300 * time.Millisecond

// Watcher wraps fsnotify and debounces filesystem events before
// forwarding them to the Indexer for re-indexing.
type Watcher struct {
	idx  *Indexer
	fw   *fsnotify.Watcher
	mu   sync.Mutex
	dirs map[string]bool

	// Recursively-watched roots. On Create dir events we add new subdirectories.
	roots map[string]bool
	rmu   sync.Mutex

	// Debounce map: path → pending timer
	pending map[string]*time.Timer
	pmx     sync.Mutex
}

func newWatcher(idx *Indexer) *Watcher {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("[watcher] cannot create fsnotify watcher: %v", err)
	}
	return &Watcher{
		idx:     idx,
		fw:      fw,
		dirs:    make(map[string]bool),
		roots:   make(map[string]bool),
		pending: make(map[string]*time.Timer),
	}
}

// Watch adds path to the fsnotify watcher. If recursive, all subdirectories
// are watched too; newly created subdirectories are picked up on the fly.
func (w *Watcher) Watch(path string, recursive bool) {
	w.mu.Lock()
	w.fw.Add(path)
	w.dirs[path] = true
	w.mu.Unlock()

	if recursive {
		w.rmu.Lock()
		w.roots[path] = true
		w.rmu.Unlock()
		w.walkAndWatch(path)
	}
}

// walkAndWatch recursively adds all subdirectories under root,
// skipping directories whose base name is in the ignored list.
func (w *Watcher) walkAndWatch(root string) {
	ignored := make(map[string]bool, len(w.idx.cfg.IgnoredDirs))
	for _, d := range w.idx.cfg.IgnoredDirs {
		ignored[d] = true
	}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if ignored[d.Name()] {
			return filepath.SkipDir
		}
		w.mu.Lock()
		if !w.dirs[p] {
			w.fw.Add(p)
			w.dirs[p] = true
		}
		w.mu.Unlock()
		return nil
	})
}

// Unwatch removes path and all its subdirectories from the watcher.
func (w *Watcher) Unwatch(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rmu.Lock()
	delete(w.roots, path)
	w.rmu.Unlock()

	// Remove all watched dirs that start with this prefix.
	for p := range w.dirs {
		if p == path || strings.HasPrefix(p, path+"/") {
			w.fw.Remove(p)
			delete(w.dirs, p)
		}
	}
}

// Run processes fsnotify events until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.fw.Close()
			return

		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handleEvent(ctx, event)

		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			log.Printf("[watcher] error: %v", err)
		}
	}
}

func (w *Watcher) handleEvent(ctx context.Context, e fsnotify.Event) {
	path := filepath.Clean(e.Name)
	ext := strings.ToLower(filepath.Ext(path))

	isIndexable := SupportedImageExts[ext] ||
		(w.idx.cfg.VideoEnabled && w.idx.ffmpegOK && SupportedVideoExts[ext])

	switch {
	case e.Op&fsnotify.Remove != 0 || e.Op&fsnotify.Rename != 0:
		// Clean up tracking if this was a watched directory (fsnotify removes
		// its watch automatically; individual file Remove events clean the DB).
		w.mu.Lock()
		delete(w.dirs, path)
		w.mu.Unlock()
		if isIndexable {
			w.cancelPending(path)
			w.idx.Events.Push(EventDeleted, path, "")
			if w.idx.cfg.PreserveOnUnmount && !w.mountOK(path) {
				log.Printf("[watcher] %s root missing or empty (unmounted?) — preserving index entry", path)
				return
			}
			go w.idx.RemoveFile(ctx, path)
		}

	case e.Op&fsnotify.Create != 0:
		// New directory under a recursive root? Watch it too.
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if w.idx.isIgnoredDir(info.Name()) {
				return
			}
			w.mu.Lock()
			already := w.dirs[path]
			w.mu.Unlock()
			if !already {
				w.rmu.Lock()
				for root := range w.roots {
					if strings.HasPrefix(path, root+"/") {
						w.mu.Lock()
						w.fw.Add(path)
						w.dirs[path] = true
						w.mu.Unlock()
						w.walkAndWatch(path)
						break
					}
				}
				w.rmu.Unlock()
			}
			return
		}
		// File created — re-index
		if !isIndexable {
			return
		}
		w.debounceWrite(ctx, path)

	case e.Op&fsnotify.Write != 0:
		if !isIndexable {
			return
		}
		w.debounceWrite(ctx, path)

	case e.Op&fsnotify.Chmod != 0:
		// Ignore permission changes
	}
}

func (w *Watcher) debounceWrite(ctx context.Context, path string) {
	w.pmx.Lock()
	defer w.pmx.Unlock()
	if t, ok := w.pending[path]; ok {
		t.Reset(debounceDelay)
	} else {
		w.pending[path] = time.AfterFunc(debounceDelay, func() {
			w.pmx.Lock()
			delete(w.pending, path)
			w.pmx.Unlock()
			w.idx.ReindexFile(ctx, path)
		})
	}
}

// mountOK checks whether the watch root containing path is still
// mounted. On Linux it consults /proc/mounts first; if the root isn't
// a mount point at all, it falls back to checking whether the directory
// exists and has entries.
func (w *Watcher) mountOK(path string) bool {
	w.rmu.Lock()
	defer w.rmu.Unlock()
	for root := range w.roots {
		if path != root && !strings.HasPrefix(path, root+"/") {
			continue
		}
		if mountedViaProcfs(root) {
			return true
		}
		ents, err := os.ReadDir(root)
		return err == nil && len(ents) > 0
	}
	return true
}

func mountedViaProcfs(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == path {
			return true
		}
	}
	return false
}

func (w *Watcher) cancelPending(path string) {
	w.pmx.Lock()
	defer w.pmx.Unlock()
	if t, ok := w.pending[path]; ok {
		t.Stop()
		delete(w.pending, path)
	}
}

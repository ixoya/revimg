// Package api implements the HTTP server and all REST handlers.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/user/revimg/internal/config"
	"github.com/user/revimg/internal/db"
	"github.com/user/revimg/internal/hasher"
	"github.com/user/revimg/internal/indexer"
	"github.com/user/revimg/internal/search"
)

// Server is the root HTTP handler.
type Server struct {
	db     *db.DB
	idx    *indexer.Indexer
	engine *search.Engine
	cfg    *config.Config
	static fs.FS
	mux    *http.ServeMux
}

// NewServer wires up all routes.
func NewServer(database *db.DB, idx *indexer.Indexer, cfg *config.Config, static fs.FS) *Server {
	s := &Server{
		db:     database,
		idx:    idx,
		engine: search.New(database, cfg),
		cfg:    cfg,
		static: static,
		mux:    http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ─── Routes ───────────────────────────────────────────────────────────────────

func (s *Server) registerRoutes() {
	// API
	s.mux.HandleFunc("POST /api/search",           s.handleSearch)
	s.mux.HandleFunc("GET  /api/files",            s.handleListFiles)
	s.mux.HandleFunc("DELETE /api/files/{id}",     s.handleDeleteFile)
	s.mux.HandleFunc("GET  /api/thumb/{id}",       s.handleThumb)
	s.mux.HandleFunc("GET  /api/open/{id}",        s.handleOpenFile)

	s.mux.HandleFunc("GET  /api/dirs",             s.handleListDirs)
	s.mux.HandleFunc("POST /api/dirs",             s.handleAddDir)
	s.mux.HandleFunc("DELETE /api/dirs/{id}",      s.handleDeleteDir)
	s.mux.HandleFunc("POST /api/dirs/{id}/scan",   s.handleRescan)
	s.mux.HandleFunc("POST /api/dirs/{id}/toggle", s.handleToggleDir)

	s.mux.HandleFunc("GET  /api/status",           s.handleStatus)
	s.mux.HandleFunc("GET  /api/stats",            s.handleStats)
	s.mux.HandleFunc("GET  /api/jobs",             s.handleJobs)

	s.mux.HandleFunc("POST /api/repair",          s.handleStartRepair)
	s.mux.HandleFunc("GET  /api/repair",          s.handleGetRepair)
	s.mux.HandleFunc("DELETE /api/repair",        s.handleCancelRepair)

	s.mux.HandleFunc("GET  /api/config",           s.handleGetConfig)
	s.mux.HandleFunc("PUT  /api/config",           s.handlePutConfig)

	s.mux.HandleFunc("GET  /api/changes",          s.handleChanges)

	// Static files (SPA fallback)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve embedded files directly, with SPA fallback
		trimmed := strings.TrimPrefix(r.URL.Path, "/")
		if trimmed == "" {
			trimmed = "index.html"
		}

		data, err := fs.ReadFile(s.static, trimmed)
		if err != nil {
			// Fall back to index.html for client-side routing
			data, err = fs.ReadFile(s.static, "index.html")
			if err != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.Contains(r.URL.Path, "/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		// Set content type based on extension
		ct := "text/html; charset=utf-8"
		if strings.HasSuffix(trimmed, ".js") {
			ct = "application/javascript"
		} else if strings.HasSuffix(trimmed, ".css") {
			ct = "text/css"
		} else if strings.HasSuffix(trimmed, ".json") {
			ct = "application/json"
		} else if strings.HasSuffix(trimmed, ".svg") {
			ct = "image/svg+xml"
		} else if strings.HasSuffix(trimmed, ".ico") {
			ct = "image/x-icon"
		} else if strings.HasSuffix(trimmed, ".png") {
			ct = "image/png"
		} else if strings.HasSuffix(trimmed, ".jpg") || strings.HasSuffix(trimmed, ".jpeg") {
			ct = "image/jpeg"
		} else if strings.HasSuffix(trimmed, ".woff2") {
			ct = "font/woff2"
		}
		w.Header().Set("Content-Type", ct)

		w.Write(data)
	})
}

// ─── Search ───────────────────────────────────────────────────────────────────

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32 MiB in-memory
		jsonError(w, "cannot parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Read search params JSON (optional field)
	var params search.Params
	if pj := r.FormValue("params"); pj != "" {
		if err := json.Unmarshal([]byte(pj), &params); err != nil {
			jsonError(w, "invalid params JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	// Apply defaults
	if params.MinSimilarity == 0 {
		params.MinSimilarity = s.cfg.DefaultThreshold
	}
	if params.MaxResults == 0 {
		params.MaxResults = s.cfg.MaxResults
	}
	if params.Weights == (search.Weights{}) {
		params.Weights = search.DefaultWeights(s.cfg)
	}

	// Read uploaded file
	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxFileSize))
	if err != nil {
		jsonError(w, "cannot read file", http.StatusInternalServerError)
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	q := &search.QueryFeatures{}

	if indexer.SupportedImageExts[ext] {
		q.FileType = "image"
		feats, err := hasher.ExtractImageFeaturesFromBytes(data, ext)
		if err != nil {
			jsonError(w, "cannot decode image: "+err.Error(), http.StatusBadRequest)
			return
		}
		q.Width = feats.Width
		q.Height = feats.Height
		q.PHash = feats.PHash
		q.AHash = feats.AHash
		q.DHash = feats.DHash
		q.ColorHistogram = feats.ColorHistogram

	} else if indexer.SupportedVideoExts[ext] {
		if !s.cfg.VideoEnabled {
			jsonError(w, "video indexing is disabled", http.StatusBadRequest)
			return
		}
		// Write to temp file for ffprobe
		tmp, err := os.CreateTemp("", "revimg-query-*"+ext)
		if err != nil {
			jsonError(w, "cannot create temp file", http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
			tmp.Close()
			jsonError(w, "cannot write temp file", http.StatusInternalServerError)
			return
		}
		tmp.Close()

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		feats, err := hasher.ExtractVideoFeatures(ctx, tmp.Name(), hasher.ProbeConfig{
			FFmpegPath:  s.cfg.FFmpegPath,
			FFprobePath: s.cfg.FFprobePath,
			SampleFPS:   s.cfg.VideoFPS,
			MaxFrames:   s.cfg.MaxFrames,
		})
		if err != nil {
			jsonError(w, "cannot probe video: "+err.Error(), http.StatusBadRequest)
			return
		}
		q.FileType = "video"
		q.Width = feats.Width
		q.Height = feats.Height
		q.PHash = feats.PHash
		q.ColorHistogram = feats.ColorHistogram
		q.VideoPHashes = feats.FrameHashes
	} else {
		jsonError(w, "unsupported file type: "+ext, http.StatusBadRequest)
		return
	}

	// Detect cross-type search from params
	if len(params.FileTypes) == 0 {
		params.FileTypes = []string{q.FileType}
	}

	resp, err := s.engine.Search(r.Context(), q, params)
	if err != nil {
		jsonError(w, "search failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, resp)
}

// ─── Files ────────────────────────────────────────────────────────────────────

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := db.FileFilter{
		PathLike: q.Get("path"),
	}
	if t := q.Get("type"); t != "" {
		filter.FileTypes = []db.FileType{db.FileType(t)}
	}
	files, err := s.db.AllFiles(r.Context(), filter)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, files)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteFileByID(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Remove thumbnail
	os.Remove(filepath.Join(s.cfg.ThumbDir(), fmt.Sprintf("%d.jpg", id)))
	jsonOK(w, map[string]bool{"ok": true})
}

// ─── Thumbnail ────────────────────────────────────────────────────────────────

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	thumbPath := filepath.Join(s.cfg.ThumbDir(), fmt.Sprintf("%d.jpg", id))

	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		// Generate on-demand
		f, err := s.db.GetFileByID(r.Context(), id)
		if err != nil || f == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if f.FileType == db.FileTypeImage {
			if err := hasher.GenerateThumbnail(f.Path, thumbPath, s.cfg.ThumbSize); err != nil {
				http.Error(w, "cannot generate thumbnail", http.StatusInternalServerError)
				return
			}
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if err := hasher.VideoThumbnail(ctx, s.cfg.FFmpegPath, f.Path, thumbPath, f.DurationMs, s.cfg.ThumbSize); err != nil {
				http.Error(w, "cannot generate thumbnail", http.StatusInternalServerError)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, thumbPath)
}

// handleOpenFile returns the raw file (for inline preview).
func (s *Server) handleOpenFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	f, err := s.db.GetFileByID(r.Context(), id)
	if err != nil || f == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, f.Path)
}

// ─── Directories ──────────────────────────────────────────────────────────────

type addDirRequest struct {
	Path       string   `json:"path"`
	Recursive  bool     `json:"recursive"`
	Extensions []string `json:"extensions"`
}

func (s *Server) handleListDirs(w http.ResponseWriter, r *http.Request) {
	dirs, err := s.db.WatchDirs(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Attach live progress
	type dirWithProgress struct {
		*db.WatchDir
		Progress *indexer.Progress `json:"progress,omitempty"`
	}
	result := make([]dirWithProgress, len(dirs))
	for i, d := range dirs {
		result[i] = dirWithProgress{
			WatchDir: d,
			Progress: s.idx.GetProgress(d.ID),
		}
	}
	jsonOK(w, result)
}

func (s *Server) handleAddDir(w http.ResponseWriter, r *http.Request) {
	var req addDirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		jsonError(w, "path is required", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(req.Path); err != nil || !info.IsDir() {
		jsonError(w, "path does not exist or is not a directory", http.StatusBadRequest)
		return
	}
	dir, err := s.idx.AddDir(r.Context(), req.Path, req.Recursive, req.Extensions)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, dir)
}

func (s *Server) handleDeleteDir(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.idx.RemoveDir(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	dirs, err := s.db.WatchDirs(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, d := range dirs {
		if d.ID == id {
			go s.idx.ScanDir(context.Background(), d)
			jsonOK(w, map[string]string{"status": "scan started"})
			return
		}
	}
	jsonError(w, "directory not found", http.StatusNotFound)
}

func (s *Server) handleToggleDir(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct{ Enabled bool `json:"enabled"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.db.ToggleWatchDir(r.Context(), id, req.Enabled); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// ─── Status / Stats ───────────────────────────────────────────────────────────

type statusResponse struct {
	Version    string                     `json:"version"`
	FFmpeg     bool                       `json:"ffmpeg_available"`
	Progress   map[int64]*indexer.Progress `json:"scans"`
	ServerTime int64                      `json:"server_time"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, statusResponse{
		Version:    "1.0.0",
		FFmpeg:     hasher.CheckFFmpeg(s.cfg.FFmpegPath, s.cfg.FFprobePath) == nil,
		Progress:   s.idx.AllProgress(),
		ServerTime: time.Now().Unix(),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.Stats(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, stats)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.db.RecentScanJobs(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, jobs)
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	n := 100
	if s := r.URL.Query().Get("n"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	jsonOK(w, s.idx.Events.Recent(n))
}

// ─── Repair ───────────────────────────────────────────────────────────────────

func (s *Server) handleStartRepair(w http.ResponseWriter, r *http.Request) {
	if !s.idx.StartRepair() {
		jsonError(w, "repair already running or disabled", http.StatusConflict)
		return
	}
	jsonOK(w, map[string]string{"status": "repair started"})
}

func (s *Server) handleGetRepair(w http.ResponseWriter, r *http.Request) {
	state, err := s.db.GetRepairState(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{
		"progress":   s.idx.RepairStatus(),
		"last_cycle": state,
	})
}

func (s *Server) handleCancelRepair(w http.ResponseWriter, r *http.Request) {
	if !s.idx.CancelRepair() {
		jsonError(w, "no repair running", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// ─── Config ───────────────────────────────────────────────────────────────────

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.cfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	// Merge incoming JSON into current config
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		jsonError(w, "cannot read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(data, s.cfg); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.cfg.Save(); err != nil {
		log.Printf("[api] cannot save config: %v", err)
	}
	jsonOK(w, s.cfg)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		log.Printf("[api] json encode: %v", err)
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

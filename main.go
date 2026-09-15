package main

import (
	"context"
	"embed"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/user/revimg/internal/api"
	"github.com/user/revimg/internal/config"
	"github.com/user/revimg/internal/db"
	"github.com/user/revimg/internal/indexer"
)

//go:embed frontend/dist
var staticFiles embed.FS

func main() {
	cfg := config.Load()

	// Log to stdout (and optionally a file)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("revimg ")
	if cfg.LogPath != "" {
		logPath := cfg.LogPath
		if !filepath.IsAbs(logPath) {
			logPath = filepath.Join(cfg.DataDir, logPath)
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Printf("warning: cannot open log file %s: %v", logPath, err)
		} else {
			log.SetOutput(io.MultiWriter(os.Stdout, f))
		}
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db %s: %v", cfg.DBPath, err)
	}
	defer database.Close()

	idx := indexer.New(database, cfg)

	static, err := fs.Sub(staticFiles, "frontend/dist")
	if err != nil {
		log.Fatalf("static files: %v", err)
	}

	srv := api.NewServer(database, idx, cfg, static)

	httpSrv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv,
		// ReadTimeout covers request headers/body (large uploads).
		ReadTimeout: 5 * time.Minute,
		// WriteTimeout & IdleTimeout left at 0 — SSE streams need to live
		// indefinitely without the server cutting them off.
	}

	go idx.Start()

	go func() {
		log.Printf("listening on http://%s", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down…")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()

	// Stop accepting new HTTP requests first.
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Drain the indexer write queue (safe to close DB after this).
	if err := idx.Shutdown(shutCtx); err != nil {
		log.Printf("indexer shutdown: %v", err)
	}
}

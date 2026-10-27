// Command reader serves an audiobook library with word-synced text.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"audiobook-reader/internal/library"
	"audiobook-reader/internal/progress"
	"audiobook-reader/internal/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	libDir := flag.String("library", "./library", "directory of forge bundles")
	dataDir := flag.String("data", defaultDataDir(), "directory for listening progress")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*addr, *libDir, *dataDir, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(addr, libDir, dataDir string, log *slog.Logger) error {
	lib, err := library.Scan(libDir, log)
	if err != nil {
		return err
	}
	store, err := progress.Open(dataDir)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           web.New(lib, store, log),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Info("listening", "addr", "http://"+addr, "books", len(lib.Books()))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// defaultDataDir follows XDG: $XDG_STATE_HOME/audiobook-reader.
func defaultDataDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return d + "/audiobook-reader"
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h + "/.local/state/audiobook-reader"
	}
	return "./state"
}

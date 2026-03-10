package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joeirimpan/nomadboard/internal/collector"
	"github.com/joeirimpan/nomadboard/internal/config"
	"github.com/joeirimpan/nomadboard/internal/server"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.huml", "path to HUML config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	log.Info("config loaded",
		"clusters", len(cfg.Clusters),
		"groups", len(cfg.Groups),
		"poll_interval", cfg.PollDuration(),
		"listen", cfg.Listen,
	)

	coll, err := collector.New(cfg, log)
	if err != nil {
		log.Error("failed to create collector", "err", err)
		os.Exit(1)
	}

	srv, err := server.New(cfg, coll, log)
	if err != nil {
		log.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	// Poll once so data is ready before serving.
	coll.Poll()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coll.Run(ctx)

	httpSrv := &http.Server{
		Addr:        cfg.Listen,
		Handler:     srv,
		ReadTimeout: 10 * time.Second,
		// No WriteTimeout; SSE connections are long-lived.
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("starting HTTP server", "addr", cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Error("HTTP server error", "err", err)
		cancel()
		os.Exit(1)
	case <-sig:
	}

	fmt.Fprintln(os.Stderr)
	log.Info("shutting down")
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	httpSrv.Shutdown(shutCtx)
}

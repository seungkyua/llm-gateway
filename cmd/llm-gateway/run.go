package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"llm-gateway/internal/config"
)

// gracefulShutdownTimeout bounds how long in-flight requests (including
// long-lived SSE streams) are given to finish after SIGTERM/SIGINT before
// the process forces shutdown (spec.md Assumptions: graceful shutdown +
// usage.json flush).
const gracefulShutdownTimeout = 10 * time.Second

// run parses flags, loads config, wires the HTTP server, and serves until
// it errors out or receives SIGTERM/SIGINT (graceful shutdown). It returns
// a process exit code rather than calling os.Exit so it stays testable.
func run(args []string) int {
	fs := flag.NewFlagSet("llm-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configFlag := fs.String("config", "", "path to config.yaml (default ./config.yaml, or LLM_GATEWAY_CONFIG env var)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "llm-gateway:", err)
		return 2
	}

	path := config.ResolvePath(*configFlag)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "llm-gateway:", err)
		return 1
	}

	engine, cleanup, err := buildEngine(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "llm-gateway:", err)
		return 1
	}
	defer cleanup()

	srv := &http.Server{Addr: cfg.Server.ListenAddr, Handler: engine}

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-serveErr:
		if err != nil {
			fmt.Fprintln(os.Stderr, "llm-gateway:", err)
			return 1
		}
		return 0
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "llm-gateway: graceful shutdown failed:", err)
			return 1
		}
		return 0
	}
}

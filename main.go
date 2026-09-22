// GPX Cartographer shows photos and GPX tracks on an OpenStreetMap map.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed time zones so the image works without tzdata
)

// version is set at build time: -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	cfg, err := loadConfig(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if cfg.ShowVersion {
		fmt.Println(version)
		return
	}
	if cfg.Healthcheck {
		os.Exit(healthcheck(cfg.Addr))
	}

	slog.Info("GPX Cartographer starting", "version", version, "addr", cfg.Addr)
	logDir("Photo directory", cfg.PhotoDir)
	logDir("GPX directory", cfg.GPXDir)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newServer(cfg).routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err := <-serveErr: // e.g. port already in use
		slog.Error("Server stopped", "error", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	// ListenAndServe returns as soon as Shutdown is called; main has to wait
	// for Shutdown itself so that running requests can finish.
	slog.Info("Shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		slog.Warn("Requests aborted during shutdown", "error", err)
	}
}

func logDir(label, dir string) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		slog.Warn(label+" not found", "path", dir)
		return
	}
	slog.Info(label, "path", dir)
}

// healthcheck queries /healthz of the locally running server.
func healthcheck(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

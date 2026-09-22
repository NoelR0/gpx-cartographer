// Cartographer zeigt Fotos und GPX-Tracks auf einer OpenStreetMap-Karte.
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
	_ "time/tzdata" // Zeitzonen einbetten, damit das Image ohne tzdata auskommt
)

// version wird beim Bauen gesetzt: -ldflags "-X main.version=1.2.3"
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

	slog.Info("Cartographer startet", "version", version, "adresse", cfg.Addr)
	logDir("Foto-Ordner", cfg.PhotoDir)
	logDir("GPX-Ordner", cfg.GPXDir)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newServer(cfg).routes(),
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

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("Server beendet", "fehler", err)
		os.Exit(1)
	}
}

func logDir(label, dir string) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		slog.Warn(label+" nicht gefunden", "pfad", dir)
		return
	}
	slog.Info(label, "pfad", dir)
}

// healthcheck fragt /healthz des lokal laufenden Servers ab.
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

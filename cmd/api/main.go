// Der API-Server der Flugsuche.
//
// Konfiguration über Umgebungsvariablen:
//
//	ADDR       Listen-Adresse (Standard ":8080")
//	PROVIDERS  Kommagetrennte Liste name=url (Standard: drei lokale Mock-Anbieter)
//	TIMEOUT    Gesamt-Timeout pro Suche (Standard "800ms")
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"flugsuche-go/internal/api"
	"flugsuche-go/internal/provider"
	"flugsuche-go/internal/search"
)

const defaultProviders = "airline-a=http://localhost:9001,airline-b=http://localhost:9002,airline-c=http://localhost:9003"

func main() {
	addr := env("ADDR", ":8080")
	timeout, err := time.ParseDuration(env("TIMEOUT", "800ms"))
	if err != nil {
		slog.Error("ungültiges TIMEOUT", "err", err)
		os.Exit(1)
	}

	providers, err := parseProviders(env("PROVIDERS", defaultProviders))
	if err != nil {
		slog.Error("ungültige PROVIDERS", "err", err)
		os.Exit(1)
	}

	svc := search.NewService(providers, timeout)
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.NewHandler(svc),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	// Sauber herunterfahren bei Strg+C / SIGTERM (wichtig für Docker/Kubernetes).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("server startet", "addr", addr, "providers", len(providers), "timeout", timeout)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("fahre herunter …")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
}

func parseProviders(s string) ([]provider.Provider, error) {
	var out []provider.Provider
	seen := make(map[string]bool)
	for _, part := range strings.Split(s, ",") {
		name, rawURL, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" || rawURL == "" {
			return nil, fmt.Errorf("erwarte name=url, bekommen: %q", part)
		}
		if seen[name] {
			return nil, fmt.Errorf("anbieter %q doppelt angegeben", name)
		}
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("anbieter %q: ungültige URL %q", name, rawURL)
		}
		seen[name] = true
		out = append(out, provider.NewHTTP(name, rawURL, nil))
	}
	return out, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

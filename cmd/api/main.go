// Der API-Server der Flugsuche.
//
// Konfiguration über Umgebungsvariablen:
//
//	ADDR       Listen-Adresse (Standard ":8080")
//	PROVIDERS  Kommagetrennte Liste name=url[|format=standard|skyjson|nested]
//	           (Standard: drei lokale Mock-Anbieter mit je eigenem Format)
//	TIMEOUT    Gesamt-Timeout pro Suche (Standard "800ms")
//	CACHE      memory | redis | off (Standard "memory")
//	CACHE_TTL  Lebensdauer eines Cache-Eintrags (Standard "5m")
//	CACHE_MAX_MB  Obergrenze für den In-Memory-Cache in MiB (Standard 64)
//	REDIS_ADDR Adresse von Redis bei CACHE=redis (Standard "localhost:6379")
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // Zeitzonen eingebettet – auch im Docker-Image ohne /usr/share/zoneinfo

	"flugsuche-go/internal/api"
	"flugsuche-go/internal/cache"
	"flugsuche-go/internal/provider"
	"flugsuche-go/internal/search"
)

const defaultProviders = "airline-a=http://localhost:9001," +
	"airline-b=http://localhost:9002|format=skyjson," +
	"airline-c=http://localhost:9003|format=nested"

func main() {
	addr := env("ADDR", ":8080")

	// "api healthcheck" für Docker: Das distroless-Image hat weder Shell noch curl.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(addr))
	}
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

	providers, closeCache, err := withCache(providers)
	if err != nil {
		slog.Error("cache-konfiguration", "err", err)
		os.Exit(1)
	}
	defer closeCache()

	svc := search.NewService(providers, timeout)
	srv := newServer(addr, api.NewHandler(svc))

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

// newServer setzt alle Timeouts. Ohne IdleTimeout bleiben Keep-Alive-Verbindungen
// unbegrenzt offen; ohne ReadTimeout kann ein Client den Request-Body endlos
// langsam senden.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// healthcheck fragt /health des laufenden Servers ab; 0 = gesund.
func healthcheck(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ADDR:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "status", resp.StatusCode)
		return 1
	}
	return 0
}

// withCache legt je nach CACHE einen Cache vor alle Anbieter.
// Die zurückgegebene Funktion gibt Ressourcen (Redis-Verbindungen) frei.
func withCache(providers []provider.Provider) ([]provider.Provider, func(), error) {
	noop := func() {}
	mode := env("CACHE", "memory")
	if mode == "off" {
		slog.Info("cache aus")
		return providers, noop, nil
	}
	ttl, err := time.ParseDuration(env("CACHE_TTL", "5m"))
	if err != nil || ttl <= 0 {
		return nil, noop, fmt.Errorf("ungültige CACHE_TTL %q", os.Getenv("CACHE_TTL"))
	}

	switch mode {
	case "memory":
		maxMB, err := strconv.Atoi(env("CACHE_MAX_MB", "64"))
		if err != nil || maxMB < 1 {
			return nil, noop, fmt.Errorf("ungültige CACHE_MAX_MB %q", os.Getenv("CACHE_MAX_MB"))
		}
		slog.Info("cache", "typ", "memory", "ttl", ttl, "max_mb", maxMB)
		return cache.WrapAll(providers, cache.NewMemory(maxMB<<20), ttl), noop, nil

	case "redis":
		addr := env("REDIS_ADDR", "localhost:6379")
		r := cache.NewRedis(addr)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Nicht abbrechen, wenn Redis (noch) nicht erreichbar ist: Die Suche funktioniert
		// auch ohne Cache, und sobald Redis da ist, wird er automatisch benutzt.
		if err := r.Ping(ctx); err != nil {
			slog.Warn("redis nicht erreichbar – suche läuft vorerst ohne cache", "addr", addr, "err", err)
		}
		slog.Info("cache", "typ", "redis", "addr", addr, "ttl", ttl)
		return cache.WrapAll(providers, r, ttl), func() { r.Close() }, nil
	}
	return nil, noop, fmt.Errorf("unbekannter CACHE %q (memory, redis, off)", mode)
}

// parseProviders liest PROVIDERS im Format
//
//	name=url[|format=<format>],name=url[|format=<format>],…
//
// Ohne format wird provider.DefaultFormat benutzt.
func parseProviders(s string) ([]provider.Provider, error) {
	var out []provider.Provider
	seen := make(map[string]bool)
	for _, entry := range strings.Split(s, ",") {
		head, opts, _ := strings.Cut(strings.TrimSpace(entry), "|")
		name, rawURL, ok := strings.Cut(head, "=")
		if !ok || name == "" || rawURL == "" {
			return nil, fmt.Errorf("erwarte name=url, bekommen: %q", entry)
		}
		if seen[name] {
			return nil, fmt.Errorf("anbieter %q doppelt angegeben", name)
		}
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("anbieter %q: ungültige URL %q", name, rawURL)
		}

		format := provider.DefaultFormat
		if opts != "" {
			for _, opt := range strings.Split(opts, "|") {
				key, val, _ := strings.Cut(opt, "=")
				switch key {
				case "format":
					format = val
				default:
					return nil, fmt.Errorf("anbieter %q: unbekannte option %q", name, key)
				}
			}
		}
		dec, err := provider.FormatByName(format)
		if err != nil {
			return nil, fmt.Errorf("anbieter %q: %w", name, err)
		}

		seen[name] = true
		out = append(out, provider.NewHTTP(provider.Config{Name: name, BaseURL: rawURL, Decoder: dec}))
	}
	return out, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

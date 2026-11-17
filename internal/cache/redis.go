package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis ist ein Store auf Basis von Redis. Mehrere API-Instanzen teilen sich so
// denselben Cache, und er überlebt einen Neustart der API.
type Redis struct {
	client *redis.Client
}

// NewRedis verbindet sich mit Redis unter addr (z. B. "localhost:6379").
// Kurze Timeouts: Ein langsames Redis darf die Suche nicht ausbremsen –
// im Zweifel wird der Anbieter direkt gefragt.
func NewRedis(addr string) *Redis {
	return &Redis{client: redis.NewClient(&redis.Options{
		Addr:          addr,
		MaxRetries:    -1, // keine Wiederholungen: lieber sofort ohne Cache weiter
		DialerRetries: 1,  // ein Verbindungsversuch statt 5 (Standard, je 100 ms Pause)
		DialTimeout:   500 * time.Millisecond,
		ReadTimeout:   100 * time.Millisecond,
		WriteTimeout:  100 * time.Millisecond,
	})}
}

// Ping prüft die Verbindung (z. B. beim Start).
func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (r *Redis) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return r.client.Set(ctx, key, val, ttl).Err()
}

// Close schließt die Verbindungen.
func (r *Redis) Close() error {
	return r.client.Close()
}

// Package cache speichert Anbieter-Antworten zwischen, damit gleiche Suchen
// nicht jedes Mal alle Anbieter abfragen.
//
// Gecacht wird pro Anbieter (Decorator um provider.Provider), nicht das
// Gesamtergebnis: Ist ein Anbieter gerade langsam oder kaputt, wird nur er beim
// nächsten Mal erneut gefragt, die anderen kommen aus dem Cache. Fehler werden
// nie gecacht.
package cache

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/provider"
)

// Store ist ein Key-Value-Speicher mit Ablaufzeit (In-Memory oder Redis).
// Werte sind Bytes, damit alle Implementierungen dasselbe speichern.
type Store interface {
	// Get liefert ok=false, wenn der Schlüssel fehlt oder abgelaufen ist.
	Get(ctx context.Context, key string) (val []byte, ok bool, err error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
}

// keyVersion wird erhöht, wenn sich das gespeicherte Format ändert –
// alte Einträge werden dann einfach nicht mehr gefunden.
const keyVersion = "v1"

// Provider ist ein Anbieter mit vorgeschaltetem Cache.
type Provider struct {
	next  provider.Provider
	store Store
	ttl   time.Duration
}

// Wrap legt einen Cache vor einen Anbieter.
func Wrap(p provider.Provider, store Store, ttl time.Duration) *Provider {
	return &Provider{next: p, store: store, ttl: ttl}
}

// WrapAll legt denselben Cache vor alle Anbieter.
func WrapAll(ps []provider.Provider, store Store, ttl time.Duration) []provider.Provider {
	out := make([]provider.Provider, len(ps))
	for i, p := range ps {
		out[i] = Wrap(p, store, ttl)
	}
	return out
}

func (c *Provider) Name() string { return c.next.Name() }

// Search liefert einen Cache-Treffer oder fragt den Anbieter und speichert
// dessen (erfolgreiche) Antwort. Ist der Cache selbst gestört (z. B. Redis weg),
// läuft die Suche ohne Cache weiter – der Cache darf die Suche nie verhindern.
func (c *Provider) Search(ctx context.Context, q flight.Query) ([]flight.Offer, error) {
	key := Key(c.next.Name(), q)

	if raw, ok, err := c.store.Get(ctx, key); err != nil {
		slog.Warn("cache lesen", "key", key, "err", err)
	} else if ok {
		var offers []flight.Offer
		if err := json.Unmarshal(raw, &offers); err == nil {
			return offers, nil
		}
		slog.Warn("cache-eintrag unlesbar", "key", key, "err", err)
	}

	offers, err := c.next.Search(ctx, q)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(offers)
	if err != nil {
		slog.Warn("cache serialisieren", "key", key, "err", err)
		return offers, nil
	}
	// Eigener Context: Auch wenn die Suche gerade in den Timeout läuft,
	// soll die bereits geholte Antwort noch gespeichert werden.
	setCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 200*time.Millisecond)
	defer cancel()
	if err := c.store.Set(setCtx, key, raw, c.ttl); err != nil {
		slog.Warn("cache schreiben", "key", key, "err", err)
	}
	return offers, nil
}

// Key baut den Cache-Schlüssel, z. B. "flugsuche:v1:airline-a:LEJ:PMI:2026-12-20".
func Key(providerName string, q flight.Query) string {
	return strings.Join([]string{"flugsuche", keyVersion, providerName, q.From, q.To, q.Date.Format(time.DateOnly)}, ":")
}

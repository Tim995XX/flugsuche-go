package cache

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"flugsuche-go/internal/flight"
)

var (
	dep   = time.Date(2026, 12, 20, 8, 0, 0, 0, time.UTC)
	query = flight.Query{From: "LEJ", To: "PMI", Date: time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)}
)

// countingProvider zählt, wie oft er wirklich gefragt wird.
type countingProvider struct {
	calls atomic.Int32
	err   error
}

func (p *countingProvider) Name() string { return "zähler" }

func (p *countingProvider) Search(_ context.Context, q flight.Query) ([]flight.Offer, error) {
	p.calls.Add(1)
	if p.err != nil {
		return nil, p.err
	}
	return []flight.Offer{{
		Provider: "zähler", FlightNumber: "AX1", From: q.From, To: q.To,
		Departure: dep, Arrival: dep.Add(2 * time.Hour), PriceCents: 5500, Currency: "EUR",
		Original: &flight.Price{AmountCents: 4701, Currency: "GBP"},
	}}, nil
}

// stores liefert alle Store-Implementierungen, damit jeder Test für beide läuft.
func stores(t *testing.T) map[string]Store {
	t.Helper()
	mr := miniredis.RunT(t)
	r := NewRedis(mr.Addr())
	t.Cleanup(func() { r.Close() })
	return map[string]Store{"memory": NewMemory(1 << 20), "redis": r}
}

func TestCacheHit(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			next := &countingProvider{}
			p := Wrap(next, store, time.Minute)

			first, err := p.Search(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			second, err := p.Search(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}

			if n := next.calls.Load(); n != 1 {
				t.Errorf("Anbieter %d-mal gefragt, want 1", n)
			}
			if len(second) != 1 || second[0].FlightNumber != first[0].FlightNumber ||
				!second[0].Departure.Equal(first[0].Departure) || second[0].Original == nil ||
				*second[0].Original != *first[0].Original {
				t.Errorf("aus dem Cache: %+v\nwant %+v", second, first)
			}

			// Andere Suche → eigener Eintrag.
			other := query
			other.Date = other.Date.AddDate(0, 0, 1)
			if _, err := p.Search(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			if n := next.calls.Load(); n != 2 {
				t.Errorf("Anbieter %d-mal gefragt, want 2", n)
			}
		})
	}
}

func TestCacheDoesNotStoreErrors(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			next := &countingProvider{err: errors.New("boom")}
			p := Wrap(next, store, time.Minute)

			for i := 0; i < 2; i++ {
				if _, err := p.Search(context.Background(), query); err == nil {
					t.Fatal("Fehler erwartet")
				}
			}
			if n := next.calls.Load(); n != 2 {
				t.Errorf("Anbieter %d-mal gefragt, want 2 (Fehler dürfen nicht gecacht werden)", n)
			}
		})
	}
}

func TestRedisTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	r := NewRedis(mr.Addr())
	defer r.Close()
	next := &countingProvider{}
	p := Wrap(next, r, time.Minute)

	p.Search(context.Background(), query)
	if ttl := mr.TTL(Key("zähler", query)); ttl != time.Minute {
		t.Errorf("TTL in Redis = %v, want 1m", ttl)
	}
	mr.FastForward(61 * time.Second)
	p.Search(context.Background(), query)
	if n := next.calls.Load(); n != 2 {
		t.Errorf("nach Ablauf: Anbieter %d-mal gefragt, want 2", n)
	}
}

func TestRedisDownFallsThrough(t *testing.T) {
	mr := miniredis.RunT(t)
	r := NewRedis(mr.Addr())
	defer r.Close()
	mr.Close() // Redis ist weg

	next := &countingProvider{}
	start := time.Now()
	offers, err := Wrap(next, r, time.Minute).Search(context.Background(), query)
	if err != nil || len(offers) != 1 {
		t.Fatalf("Suche muss ohne Cache weiterlaufen: offers=%v err=%v", offers, err)
	}
	// Ohne MaxRetries/DialerRetries-Begrenzung dauerte das ~2 s – bei 800 ms Such-Timeout fatal.
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("Ausgefallenes Redis bremst die Suche um %v aus", d)
	}
}

func TestCorruptEntryIsIgnored(t *testing.T) {
	store := NewMemory(1 << 20)
	store.Set(context.Background(), Key("zähler", query), []byte("kein json"), time.Minute)

	next := &countingProvider{}
	offers, err := Wrap(next, store, time.Minute).Search(context.Background(), query)
	if err != nil || len(offers) != 1 || next.calls.Load() != 1 {
		t.Fatalf("offers=%v err=%v calls=%d", offers, err, next.calls.Load())
	}
}

func TestMemoryTTLAndEviction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewMemory(4) // jeder Eintrag hier: 1 Byte Schlüssel + 1 Byte Wert → Platz für 2
	m.now = func() time.Time { return now }

	m.Set(ctx, "a", []byte("1"), time.Minute)
	if _, ok, _ := m.Get(ctx, "a"); !ok {
		t.Fatal("a sollte da sein")
	}

	now = now.Add(time.Minute)
	if _, ok, _ := m.Get(ctx, "a"); ok {
		t.Fatal("a sollte genau nach TTL abgelaufen sein")
	}
	if m.Len() != 0 || m.Bytes() != 0 {
		t.Errorf("Len = %d, Bytes = %d, abgelaufener Eintrag sollte beim Lesen entfernt werden", m.Len(), m.Bytes())
	}

	m.Set(ctx, "b", []byte("2"), time.Second)
	m.Set(ctx, "c", []byte("3"), time.Hour)
	now = now.Add(2 * time.Second) // b ist abgelaufen
	m.Set(ctx, "d", []byte("4"), time.Hour)
	if m.Len() != 2 {
		t.Errorf("Len = %d, want 2", m.Len())
	}
	if _, ok, _ := m.Get(ctx, "c"); !ok {
		t.Error("c ist noch gültig und hätte nicht verdrängt werden dürfen (b war abgelaufen)")
	}

	m.Set(ctx, "e", []byte("5"), time.Hour) // voll, nichts abgelaufen → einer muss weichen
	if m.Len() != 2 {
		t.Errorf("Len = %d, want 2 (Obergrenze)", m.Len())
	}
	if v, ok, _ := m.Get(ctx, "e"); !ok || string(v) != "5" {
		t.Error("neuer Eintrag e fehlt")
	}

	// Überschreiben eines vorhandenen Schlüssels verdrängt nichts.
	m.Set(ctx, "e", []byte("6"), time.Hour)
	if m.Len() != 2 || m.Bytes() != 4 {
		t.Errorf("Len = %d, Bytes = %d nach Überschreiben, want 2 / 4", m.Len(), m.Bytes())
	}
}

func TestMemoryByteLimit(t *testing.T) {
	ctx := context.Background()
	const limit = 10 << 20 // 10 MiB
	m := NewMemory(limit)

	big := make([]byte, 1<<20) // 1 MiB – so groß darf eine Anbieter-Antwort sein
	for i := 0; i < 100; i++ {
		m.Set(ctx, fmt.Sprintf("k%d", i), big, time.Hour)
	}
	if n := m.Len(); n > 10 || m.Bytes() > limit {
		t.Errorf("%d Einträge à 1 MiB (%d Bytes) im Cache – Grenze von 10 MiB überschritten", n, m.Bytes())
	}
	if _, ok, _ := m.Get(ctx, "k99"); !ok {
		t.Error("der zuletzt geschriebene Eintrag sollte im Cache sein")
	}

	// Ein Eintrag, der allein größer als das Limit ist, wird gar nicht erst gespeichert.
	if err := m.Set(ctx, "riesig", make([]byte, limit+1), time.Hour); err == nil {
		t.Error("zu großer Eintrag: Fehler erwartet")
	}
	if _, ok, _ := m.Get(ctx, "riesig"); ok {
		t.Error("zu großer Eintrag wurde gespeichert")
	}
}

func TestMemoryCopiesValue(t *testing.T) {
	m := NewMemory(1 << 20)
	val := []byte("abc")
	m.Set(context.Background(), "k", val, time.Minute)
	val[0] = 'X'
	if got, _, _ := m.Get(context.Background(), "k"); string(got) != "abc" {
		t.Errorf("Cache wurde von außen verändert: %q", got)
	}
}

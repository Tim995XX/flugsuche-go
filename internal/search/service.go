// Package search fragt alle Anbieter parallel ab und führt die Ergebnisse zusammen.
package search

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"sort"
	"time"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/provider"
)

// ProviderError beschreibt einen Anbieter, der nicht (rechtzeitig) geantwortet hat.
// Error ist absichtlich nur eine grobe Kategorie ("timeout", "abgebrochen",
// "nicht verfügbar") – die Details (interne URLs, Hostnamen) stehen nur im Log.
type ProviderError struct {
	Provider string `json:"provider"`
	Error    string `json:"error"`
}

// Result ist das zusammengeführte Suchergebnis.
type Result struct {
	Offers []flight.Offer  `json:"offers"`
	Errors []ProviderError `json:"errors,omitempty"`
}

// Service orchestriert die Suche über alle Anbieter.
type Service struct {
	providers []provider.Provider
	timeout   time.Duration
}

// NewService erzeugt einen Such-Service. timeout begrenzt die Gesamtdauer einer Suche.
func NewService(providers []provider.Provider, timeout time.Duration) *Service {
	return &Service{providers: providers, timeout: timeout}
}

type providerResult struct {
	idx    int
	offers []flight.Offer
	err    error
}

// Search fragt alle Anbieter gleichzeitig ab. Langsame oder fehlerhafte Anbieter
// blockieren das Ergebnis nicht – sie landen in Result.Errors.
//
// Das Timeout wird hier durchgesetzt, nicht nur an die Anbieter weitergereicht:
// Auch ein Anbieter, der den Context ignoriert, kann die Antwort nicht verzögern.
func (s *Service) Search(ctx context.Context, q flight.Query) Result {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Gepuffert: Anbieter, die nach dem Timeout fertig werden, können ihr Ergebnis
	// noch abliefern und beenden sich – es bleibt keine Goroutine hängen.
	results := make(chan providerResult, len(s.providers))
	for i, p := range s.providers {
		go func(i int, p provider.Provider) {
			offers, err := safeSearch(ctx, p, q)
			results <- providerResult{idx: i, offers: offers, err: err}
		}(i, p)
	}

	var all []flight.Offer
	var errs []ProviderError
	answered := make([]bool, len(s.providers))
	fail := func(i int, err error) {
		name := s.providers[i].Name()
		slog.Warn("anbieter fehlgeschlagen", "provider", name, "err", err)
		errs = append(errs, ProviderError{Provider: name, Error: publicError(err)})
	}
	collect := func(r providerResult) {
		answered[r.idx] = true
		if r.err != nil {
			fail(r.idx, r.err)
			return
		}
		all = append(all, r.offers...)
	}

	for pending := len(s.providers); pending > 0; pending-- {
		select {
		case r := <-results:
			collect(r)
			continue
		case <-ctx.Done():
		}
		// Zeit abgelaufen: Wer noch nicht geantwortet hat, gilt als Timeout (bzw. abgebrochen).
		for i := range s.providers {
			if !answered[i] {
				fail(i, ctx.Err())
			}
		}
		break
	}

	offers := dedupe(all)
	sort.SliceStable(offers, func(i, j int) bool {
		return offers[i].PriceCents < offers[j].PriceCents
	})
	if offers == nil {
		offers = []flight.Offer{} // JSON: [] statt null
	}
	return Result{Offers: offers, Errors: errs}
}

// publicError übersetzt einen internen Fehler in eine Kategorie für den Client.
func publicError(err error) string {
	var t interface{ Timeout() bool } // z. B. http.Client.Timeout, net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &t) && t.Timeout():
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "abgebrochen"
	default:
		return "nicht verfügbar"
	}
}

// errProviderPanic wird gemeldet, wenn ein Anbieter abgestürzt ist.
var errProviderPanic = errors.New("interner fehler im anbieter")

// safeSearch ruft den Anbieter auf und fängt einen Panic ab. Ohne das würde ein
// einziger Bug in einem Adapter den ganzen Server beenden: net/http fängt Panics
// nur im Handler-Goroutine ab, nicht in den Goroutinen, die wir hier starten.
func safeSearch(ctx context.Context, p provider.Provider, q flight.Query) (offers []flight.Offer, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("anbieter abgestürzt", "provider", p.Name(), "panic", r, "stack", string(debug.Stack()))
			offers, err = nil, errProviderPanic
		}
	}()
	return p.Search(ctx, q)
}

// dedupe entfernt Angebote für denselben Flug (Flugnummer + Abflugzeit)
// und behält jeweils das günstigste.
func dedupe(offers []flight.Offer) []flight.Offer {
	type key struct {
		number    string
		departure time.Time
	}
	best := make(map[key]int) // key -> Index in out
	var out []flight.Offer

	for _, o := range offers {
		k := key{o.FlightNumber, o.Departure.UTC()}
		if i, ok := best[k]; ok {
			if o.PriceCents < out[i].PriceCents {
				out[i] = o
			}
			continue
		}
		best[k] = len(out)
		out = append(out, o)
	}
	return out
}

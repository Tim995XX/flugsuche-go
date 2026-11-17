// Package search fragt alle Anbieter parallel ab und führt die Ergebnisse zusammen.
package search

import (
	"context"
	"sort"
	"sync"
	"time"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/provider"
)

// ProviderError beschreibt einen Anbieter, der nicht (rechtzeitig) geantwortet hat.
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
	name   string
	offers []flight.Offer
	err    error
}

// Search fragt alle Anbieter gleichzeitig ab. Langsame oder fehlerhafte Anbieter
// blockieren das Ergebnis nicht – sie landen in Result.Errors.
func (s *Service) Search(ctx context.Context, q flight.Query) Result {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	results := make(chan providerResult, len(s.providers))
	var wg sync.WaitGroup

	for _, p := range s.providers {
		wg.Add(1)
		go func(p provider.Provider) {
			defer wg.Done()
			offers, err := p.Search(ctx, q)
			results <- providerResult{name: p.Name(), offers: offers, err: err}
		}(p)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var all []flight.Offer
	var errs []ProviderError
	for r := range results {
		if r.err != nil {
			errs = append(errs, ProviderError{Provider: r.name, Error: r.err.Error()})
			continue
		}
		all = append(all, r.offers...)
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

package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/provider"
)

// fakeProvider ist ein Test-Double, das ohne Netzwerk auskommt.
type fakeProvider struct {
	name   string
	offers []flight.Offer
	err    error
	delay  time.Duration
}

func (f fakeProvider) Name() string { return f.name }

func (f fakeProvider) Search(ctx context.Context, _ flight.Query) ([]flight.Offer, error) {
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.offers, f.err
}

var dep = time.Date(2026, 12, 20, 8, 0, 0, 0, time.UTC)

func offer(provider, number string, price int) flight.Offer {
	return flight.Offer{Provider: provider, FlightNumber: number, Departure: dep, Arrival: dep.Add(2 * time.Hour), PriceCents: price}
}

func TestSearch(t *testing.T) {
	tests := []struct {
		name       string
		providers  []provider.Provider
		wantPrices []int
		wantErrs   int
	}{
		{
			name: "sortiert nach preis",
			providers: []provider.Provider{
				fakeProvider{name: "a", offers: []flight.Offer{offer("a", "X1", 300), offer("a", "X2", 100)}},
				fakeProvider{name: "b", offers: []flight.Offer{offer("b", "X3", 200)}},
			},
			wantPrices: []int{100, 200, 300},
		},
		{
			name: "fehlerhafter anbieter blockiert nicht",
			providers: []provider.Provider{
				fakeProvider{name: "a", offers: []flight.Offer{offer("a", "X1", 100)}},
				fakeProvider{name: "kaputt", err: errors.New("boom")},
			},
			wantPrices: []int{100},
			wantErrs:   1,
		},
		{
			name: "langsamer anbieter läuft in timeout",
			providers: []provider.Provider{
				fakeProvider{name: "a", offers: []flight.Offer{offer("a", "X1", 100)}},
				fakeProvider{name: "lahm", offers: []flight.Offer{offer("lahm", "X9", 1)}, delay: time.Second},
			},
			wantPrices: []int{100},
			wantErrs:   1,
		},
		{
			name: "duplikate: günstigstes gewinnt",
			providers: []provider.Provider{
				fakeProvider{name: "a", offers: []flight.Offer{offer("a", "X1", 250)}},
				fakeProvider{name: "b", offers: []flight.Offer{offer("b", "X1", 199)}},
			},
			wantPrices: []int{199},
		},
		{
			name: "gleiche flugnummer, anderer tag: kein duplikat",
			providers: []provider.Provider{
				fakeProvider{name: "a", offers: []flight.Offer{
					offer("a", "X1", 250),
					func() flight.Offer { o := offer("a", "X1", 199); o.Departure = dep.Add(24 * time.Hour); return o }(),
				}},
			},
			wantPrices: []int{199, 250},
		},
		{
			name:       "keine anbieter",
			providers:  nil,
			wantPrices: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.providers, 50*time.Millisecond)
			res := svc.Search(context.Background(), flight.Query{From: "LEJ", To: "PMI", Date: dep})

			if len(res.Errors) != tt.wantErrs {
				t.Errorf("errors = %d, want %d (%v)", len(res.Errors), tt.wantErrs, res.Errors)
			}
			if len(res.Offers) != len(tt.wantPrices) {
				t.Fatalf("offers = %d, want %d", len(res.Offers), len(tt.wantPrices))
			}
			for i, want := range tt.wantPrices {
				if got := res.Offers[i].PriceCents; got != want {
					t.Errorf("offers[%d].PriceCents = %d, want %d", i, got, want)
				}
			}
		})
	}
}

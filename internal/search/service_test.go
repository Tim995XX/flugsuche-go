package search

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

// panicProvider simuliert einen Bug in einem Adapter.
type panicProvider struct{}

func (panicProvider) Name() string { return "kaputt" }

func (panicProvider) Search(context.Context, flight.Query) ([]flight.Offer, error) {
	var m map[string]int
	m["boom"] = 1 // panic: assignment to entry in nil map
	return nil, nil
}

func TestSearchSurvivesProviderPanic(t *testing.T) {
	svc := NewService([]provider.Provider{
		fakeProvider{name: "a", offers: []flight.Offer{offer("a", "X1", 100)}},
		panicProvider{},
	}, time.Second)

	res := svc.Search(context.Background(), flight.Query{From: "LEJ", To: "PMI", Date: dep})

	if len(res.Offers) != 1 {
		t.Errorf("offers = %d, want 1 (gesunder Anbieter muss trotzdem liefern)", len(res.Offers))
	}
	if len(res.Errors) != 1 || res.Errors[0].Provider != "kaputt" {
		t.Errorf("errors = %+v, want 1 Fehler von \"kaputt\"", res.Errors)
	}
}

func TestProviderErrorsHideInternals(t *testing.T) {
	internal := errors.New(`airline-x: anfrage fehlgeschlagen: Get "http://interner-host.local:9003/flights": connection refused`)
	svc := NewService([]provider.Provider{
		fakeProvider{name: "defekt", err: internal},
		fakeProvider{name: "lahm", delay: time.Second},
		panicProvider{}, // Name "kaputt"
	}, 50*time.Millisecond)

	res := svc.Search(context.Background(), flight.Query{From: "LEJ", To: "PMI", Date: dep})

	want := map[string]string{"defekt": "nicht verfügbar", "lahm": "timeout", "kaputt": "nicht verfügbar"}
	got := map[string]string{}
	for _, e := range res.Errors {
		got[e.Provider] = e.Error
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("Fehler für %s = %q, want %q", p, got[p], w)
		}
	}
}

func TestPublicError(t *testing.T) {
	timeoutErr := &url.Error{Op: "Get", URL: "http://x", Err: timeoutError{}}
	tests := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{fmt.Errorf("x: antwort dekodieren: %w", context.DeadlineExceeded), "timeout"},
		{fmt.Errorf("x: anfrage fehlgeschlagen: %w", timeoutErr), "timeout"}, // http.Client.Timeout
		{fmt.Errorf("x: %w", context.Canceled), "abgebrochen"},
		{errors.New("x: unerwarteter status 503"), "nicht verfügbar"},
	}
	for _, tt := range tests {
		if got := publicError(tt.err); got != tt.want {
			t.Errorf("publicError(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

type timeoutError struct{}

func (timeoutError) Error() string { return "Client.Timeout exceeded" }
func (timeoutError) Timeout() bool { return true }

// stubbornProvider ignoriert den Context – so wie ein schlecht geschriebener Adapter.
type stubbornProvider struct{ sleep time.Duration }

func (stubbornProvider) Name() string { return "stur" }

func (p stubbornProvider) Search(context.Context, flight.Query) ([]flight.Offer, error) {
	time.Sleep(p.sleep)
	return []flight.Offer{offer("stur", "S1", 1)}, nil
}

func TestTimeoutIsEnforced(t *testing.T) {
	svc := NewService([]provider.Provider{
		fakeProvider{name: "a", offers: []flight.Offer{offer("a", "X1", 100)}},
		stubbornProvider{sleep: time.Second},
	}, 50*time.Millisecond)

	start := time.Now()
	res := svc.Search(context.Background(), flight.Query{From: "LEJ", To: "PMI", Date: dep})
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Errorf("Suche dauerte %v trotz 50 ms Timeout", d)
	}
	if len(res.Offers) != 1 || res.Offers[0].Provider != "a" {
		t.Errorf("offers = %+v, want nur das Angebot von a", res.Offers)
	}
	if len(res.Errors) != 1 || res.Errors[0].Provider != "stur" || res.Errors[0].Error != "timeout" {
		t.Errorf("errors = %+v, want stur: timeout", res.Errors)
	}
}

func TestClientCancelIsReported(t *testing.T) {
	svc := NewService([]provider.Provider{stubbornProvider{sleep: time.Second}}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel) // Nutzer schließt den Browser-Tab

	res := svc.Search(ctx, flight.Query{From: "LEJ", To: "PMI", Date: dep})
	if len(res.Errors) != 1 || res.Errors[0].Error != "abgebrochen" {
		t.Errorf("errors = %+v, want stur: abgebrochen", res.Errors)
	}
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

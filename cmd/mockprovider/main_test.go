package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/provider"
)

// TestMockMatchesAdapters stellt sicher, dass Mock-Anbieter und Adapter zusammenpassen:
// Für jedes Format liefert der Mock Flüge, der HTTPProvider liest sie über den echten
// HTTP-Weg ein, und am Ende kommen dieselben Flüge mit (fast) denselben EUR-Preisen heraus.
func TestMockMatchesAdapters(t *testing.T) {
	q := flight.Query{From: "LEJ", To: "PMI", Date: time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)}

	for _, format := range provider.FormatNames() {
		t.Run(format, func(t *testing.T) {
			srv := httptest.NewServer(newHandler("airline-"+format, 0, 0, format))
			defer srv.Close()

			dec, err := provider.FormatByName(format)
			if err != nil {
				t.Fatal(err)
			}
			got, err := provider.NewHTTP(provider.Config{Name: format, BaseURL: srv.URL, Decoder: dec}).Search(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}

			want := generate("airline-"+format, q.From, q.To, q.Date)
			if len(got) != len(want) {
				t.Fatalf("len = %d, want %d", len(got), len(want))
			}
			for i := range want {
				g, w := got[i], want[i]
				if g.FlightNumber != w.FlightNumber || g.From != w.From || g.To != w.To ||
					!g.Departure.Equal(w.Departure) || !g.Arrival.Equal(w.Arrival) {
					t.Errorf("[%d] got %+v\nwant %+v", i, g, w)
				}
				// Hin- und Rückumrechnung darf höchstens 1 Cent abweichen.
				if d := g.PriceCents - w.PriceCents; d < -1 || d > 1 || g.Currency != "EUR" {
					t.Errorf("[%d] Preis = %d %s, want ≈ %d EUR", i, g.PriceCents, g.Currency, w.PriceCents)
				}
				if (format == "standard") != (g.Original == nil) {
					t.Errorf("[%d] Original = %+v", i, g.Original)
				}
			}
		})
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	if _, err := render("xml", nil); err == nil {
		t.Error("Fehler erwartet")
	}
}

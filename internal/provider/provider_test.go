package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flugsuche-go/internal/currency"
	"flugsuche-go/internal/flight"
)

var query = flight.Query{From: "LEJ", To: "PMI", Date: time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)}

// standardOffer liefert ein gültiges Angebot im Standardformat; mit set lassen sich Felder überschreiben.
func standardOffer(set ...string) string {
	f := map[string]string{
		"flight_number": `"AX1"`, "from": `"LEJ"`, "to": `"PMI"`,
		"departure": `"2026-12-20T08:00:00Z"`, "arrival": `"2026-12-20T10:00:00Z"`,
		"price_cents": `4999`, "currency": `"EUR"`, "provider": `"gefälscht"`,
	}
	for i := 0; i+1 < len(set); i += 2 {
		if set[i+1] == "" {
			delete(f, set[i])
		} else {
			f[set[i]] = set[i+1]
		}
	}
	var parts []string
	for k, v := range f {
		parts = append(parts, `"`+k+`":`+v)
	}
	return "[{" + strings.Join(parts, ",") + "}]"
}

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPProviderSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flights" {
			t.Errorf("path = %q, want /flights", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("from") != "LEJ" || q.Get("to") != "PMI" || q.Get("date") != "2026-12-20" {
			t.Errorf("query = %v", q)
		}
		w.Write([]byte(standardOffer()))
	}))
	defer srv.Close()

	// Abschließender Slash darf nicht zu "//flights" führen.
	p := NewHTTP(Config{Name: "airline-x", BaseURL: srv.URL + "/"})
	offers, err := p.Search(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].FlightNumber != "AX1" || offers[0].PriceCents != 4999 {
		t.Fatalf("offers = %+v", offers)
	}
	if offers[0].Provider != "airline-x" {
		t.Errorf("Provider = %q, want airline-x (vom Anbieter gemeldeter Name wird überschrieben)", offers[0].Provider)
	}
	if offers[0].Original != nil {
		t.Errorf("Original = %+v, bei EUR sollte nichts umgerechnet werden", offers[0].Original)
	}
}

func TestHTTPProviderConvertsCurrency(t *testing.T) {
	srv := serve(t, standardOffer("price_cents", "10000", "currency", `"usd"`))
	p := NewHTTP(Config{Name: "x", BaseURL: srv.URL, Converter: currency.FixedRates{"USD": 920_000}})

	offers, err := p.Search(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	o := offers[0]
	if o.PriceCents != 9200 || o.Currency != "EUR" {
		t.Errorf("Preis = %d %s, want 9200 EUR", o.PriceCents, o.Currency)
	}
	if o.Original == nil || *o.Original != (flight.Price{AmountCents: 10000, Currency: "USD"}) {
		t.Errorf("Original = %+v, want 10000 USD", o.Original)
	}
}

func TestHTTPProviderFillsRouteFromQuery(t *testing.T) {
	srv := serve(t, standardOffer("from", "", "to", `"pmi"`))
	offers, err := NewHTTP(Config{Name: "x", BaseURL: srv.URL}).Search(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if offers[0].From != "LEJ" || offers[0].To != "PMI" {
		t.Errorf("Strecke = %s-%s, want LEJ-PMI", offers[0].From, offers[0].To)
	}
}

func TestHTTPProviderErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name:    "status 503",
			handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusServiceUnavailable) },
			wantErr: "unerwarteter status 503",
		},
		{
			name:    "kaputtes json",
			handler: func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`[{"flight_number":`)) },
			wantErr: "antwort dekodieren",
		},
		{
			name: "zu große antwort",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`[{"flight_number":"` + strings.Repeat("x", maxResponseBytes) + `"}]`))
			},
			wantErr: "antwort dekodieren",
		},
	}

	invalid := []struct{ name, body, wantErr string }{
		{"flugnummer fehlt", standardOffer("flight_number", ""), "flugnummer fehlt"},
		{"falsche strecke", standardOffer("to", `"BCN"`), "falsche strecke LEJ-BCN"},
		{"abflug fehlt", standardOffer("departure", ""), "abflugzeit fehlt"},
		{"ankunft vor abflug", standardOffer("arrival", `"2026-12-20T07:00:00Z"`), "ankunft liegt nicht nach"},
		{"preis null", standardOffer("price_cents", "0"), "ungültiger preis"},
		{"preis negativ", standardOffer("price_cents", "-100"), "ungültiger preis"},
		{"währung fehlt", standardOffer("currency", ""), "währung fehlt"},
		{"währung unbekannt", standardOffer("currency", `"XYZ"`), "unbekannte währung"},
	}
	for _, iv := range invalid {
		body := iv.body
		tests = append(tests, struct {
			name    string
			handler http.HandlerFunc
			wantErr string
		}{iv.name, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }, iv.wantErr})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			_, err := NewHTTP(Config{Name: "x", BaseURL: srv.URL}).Search(context.Background(), query)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want enthält %q", err, tt.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "x: ") {
				t.Errorf("Fehler sollte mit Anbieternamen beginnen: %v", err)
			}
		})
	}
}

func TestHTTPProviderUnknownCurrencyIsWrapped(t *testing.T) {
	srv := serve(t, standardOffer("currency", `"XYZ"`))
	_, err := NewHTTP(Config{Name: "x", BaseURL: srv.URL}).Search(context.Background(), query)
	if !errors.Is(err, currency.ErrUnknown) {
		t.Errorf("err = %v, want errors.Is(currency.ErrUnknown)", err)
	}
}

func TestHTTPProviderUsesDecoder(t *testing.T) {
	srv := serve(t, `{"data":{"currency":"EUR","flights":[{"carrier":"CX","number":7,
		"segment":{"from":"LEJ","to":"PMI","dep":1797753600,"arr":1797760800},"fare":{"total_minor":3900}}]}}`)
	dec, _ := FormatByName("nested")

	offers, err := NewHTTP(Config{Name: "c", BaseURL: srv.URL, Decoder: dec}).Search(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if offers[0].FlightNumber != "CX7" || offers[0].PriceCents != 3900 || offers[0].Provider != "c" {
		t.Errorf("offers = %+v", offers)
	}
}

func TestHTTPProviderContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewHTTP(Config{Name: "lahm", BaseURL: srv.URL}).Search(ctx, query)
	if err == nil {
		t.Fatal("erwartet Fehler bei abgelaufenem Context")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Abbruch dauerte %v – Context wird nicht beachtet", d)
	}
}

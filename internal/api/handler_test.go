package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/provider"
	"flugsuche-go/internal/search"
)

// Feste "heute"-Zeit, damit die Tests nicht irgendwann wegen des echten Datums brechen.
var fixedNow = time.Date(2026, 11, 17, 15, 30, 0, 0, time.UTC)

func withFixedNow(t *testing.T) {
	t.Helper()
	now = func() time.Time { return fixedNow }
	t.Cleanup(func() { now = time.Now })
}

func TestSearchValidation(t *testing.T) {
	withFixedNow(t)
	h := NewHandler(search.NewService(nil, time.Second))

	tests := []struct {
		query string
		want  int
	}{
		{"from=LEJ&to=PMI&date=2026-12-20", http.StatusOK},
		{"from=lej&to=pmi&date=2026-12-20", http.StatusOK}, // Kleinschreibung wird normalisiert
		{"from=LEJ&to=PMI&date=2026-11-17", http.StatusOK}, // heute ist erlaubt
		{"from=LEJ&to=PMI&date=2027-11-17", http.StatusOK}, // genau 365 Tage
		{"from=LEJ&to=PMI&date=2026-11-16", http.StatusBadRequest},
		{"from=LEJ&to=PMI&date=2027-11-18", http.StatusBadRequest},
		{"from=LEJ&to=LEJ&date=2026-12-20", http.StatusBadRequest},
		{"from=LEIPZIG&to=PMI&date=2026-12-20", http.StatusBadRequest},
		{"from=LEJ&to=PMI&date=20.12.2026", http.StatusBadRequest},
		{"", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?"+tt.query, nil))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.want, rec.Body)
			}
		})
	}
}

type stubProvider struct {
	gotQuery *flight.Query
	offers   []flight.Offer
}

func (s stubProvider) Name() string { return "stub" }

func (s stubProvider) Search(_ context.Context, q flight.Query) ([]flight.Offer, error) {
	*s.gotQuery = q
	return s.offers, nil
}

func TestSearchResponseBody(t *testing.T) {
	withFixedNow(t)
	dep := time.Date(2026, 12, 20, 8, 0, 0, 0, time.UTC)
	var got flight.Query
	stub := stubProvider{gotQuery: &got, offers: []flight.Offer{
		{Provider: "stub", FlightNumber: "AX1", Departure: dep, Arrival: dep.Add(2 * time.Hour), PriceCents: 4999, Currency: "EUR"},
	}}
	h := NewHandler(search.NewService([]provider.Provider{stub}, time.Second))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?from=lej&to=pmi&date=2026-12-20", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if got.From != "LEJ" || got.To != "PMI" || !got.Date.Equal(time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Anbieter bekam Query %+v", got)
	}

	var body search.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if len(body.Offers) != 1 || body.Offers[0].FlightNumber != "AX1" || body.Offers[0].PriceCents != 4999 {
		t.Errorf("offers = %+v", body.Offers)
	}
	if len(body.Errors) != 0 {
		t.Errorf("errors = %+v", body.Errors)
	}
}

func TestSearchEmptyResultIsArray(t *testing.T) {
	withFixedNow(t)
	h := NewHandler(search.NewService(nil, time.Second))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?from=LEJ&to=PMI&date=2026-12-20", nil))

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if string(raw["offers"]) != "[]" {
		t.Errorf("offers = %s, want []", raw["offers"])
	}
}

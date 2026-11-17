package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flugsuche-go/internal/flight"
)

var query = flight.Query{From: "LEJ", To: "PMI", Date: time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)}

func TestHTTPProviderSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flights" {
			t.Errorf("path = %q, want /flights", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("from") != "LEJ" || q.Get("to") != "PMI" || q.Get("date") != "2026-12-20" {
			t.Errorf("query = %v", q)
		}
		w.Write([]byte(`[{"flight_number":"AX1","price_cents":4999,"currency":"EUR","provider":"gefälscht"}]`))
	}))
	defer srv.Close()

	// Abschließender Slash darf nicht zu "//flights" führen.
	p := NewHTTP("airline-x", srv.URL+"/", nil)
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			_, err := NewHTTP("x", srv.URL, nil).Search(context.Background(), query)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want enthält %q", err, tt.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "x: ") {
				t.Errorf("Fehler sollte mit Anbieternamen beginnen: %v", err)
			}
		})
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
	_, err := NewHTTP("lahm", srv.URL, nil).Search(ctx, query)
	if err == nil {
		t.Fatal("erwartet Fehler bei abgelaufenem Context")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Abbruch dauerte %v – Context wird nicht beachtet", d)
	}
}

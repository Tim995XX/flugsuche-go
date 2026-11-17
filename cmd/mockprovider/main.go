// Ein simulierter Airline-Anbieter für lokale Entwicklung und Tests.
//
// Er erzeugt deterministische Fake-Flüge und kann künstlich langsam
// oder fehlerhaft sein – genau wie echte externe APIs.
//
//	go run ./cmd/mockprovider -name airline-a -port 9001 -delay 200ms -failrate 0.1
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"flugsuche-go/internal/flight"
)

func main() {
	name := flag.String("name", "airline-a", "Name des Anbieters (beeinflusst Flugnummern und Preise)")
	port := flag.Int("port", 9001, "Port")
	delay := flag.Duration("delay", 150*time.Millisecond, "maximale künstliche Antwortzeit")
	failRate := flag.Float64("failrate", 0.0, "Anteil der Anfragen, die mit 503 fehlschlagen (0.0–1.0)")
	flag.Parse()

	if *name == "" || *delay < 0 || *failRate < 0 || *failRate > 1 {
		fmt.Fprintln(os.Stderr, "ungültige Flags: -name darf nicht leer sein, -delay ≥ 0, -failrate zwischen 0 und 1")
		os.Exit(2)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /flights", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(rand.Int63n(int64(*delay) + 1)))

		if rand.Float64() < *failRate {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		q := r.URL.Query()
		date, err := time.Parse(time.DateOnly, q.Get("date"))
		if err != nil {
			http.Error(w, "bad date", http.StatusBadRequest)
			return
		}
		offers := generate(*name, strings.ToUpper(q.Get("from")), strings.ToUpper(q.Get("to")), date)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(offers)
	})

	addr := fmt.Sprintf(":%d", *port)
	slog.Info("mock-anbieter startet", "name", *name, "addr", addr, "delay", *delay, "failrate", *failRate)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// generate erzeugt für dieselbe Anfrage immer dieselben Flüge (Seed aus den Parametern).
func generate(provider, from, to string, date time.Time) []flight.Offer {
	h := fnv.New64a()
	fmt.Fprint(h, provider, from, to, date.Format(time.DateOnly))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	prefix := strings.ToUpper(provider[len(provider)-1:]) + "X"
	n := 2 + rng.Intn(4)
	offers := make([]flight.Offer, 0, n)
	for i := 0; i < n; i++ {
		dep := date.Add(time.Duration(6+rng.Intn(16)) * time.Hour).Add(time.Duration(rng.Intn(4)*15) * time.Minute)
		offers = append(offers, flight.Offer{
			FlightNumber: fmt.Sprintf("%s%d", prefix, 100+rng.Intn(900)),
			From:         from,
			To:           to,
			Departure:    dep,
			Arrival:      dep.Add(time.Duration(60+rng.Intn(240)) * time.Minute),
			PriceCents:   (4900 + rng.Intn(40000)) / 100 * 100,
			Currency:     "EUR",
		})
	}
	return offers
}

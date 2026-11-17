// Ein simulierter Airline-Anbieter für lokale Entwicklung und Tests.
//
// Er erzeugt deterministische Fake-Flüge und kann künstlich langsam
// oder fehlerhaft sein – genau wie echte externe APIs. Mit -format antwortet
// er in einem der Formate, die die Adapter in internal/provider verstehen.
//
//	go run ./cmd/mockprovider -name airline-b -port 9002 -delay 400ms -failrate 0.1 -format skyjson
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
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/provider"
)

func main() {
	name := flag.String("name", "airline-a", "Name des Anbieters (beeinflusst Flugnummern und Preise)")
	port := flag.Int("port", 9001, "Port")
	delay := flag.Duration("delay", 150*time.Millisecond, "maximale künstliche Antwortzeit")
	failRate := flag.Float64("failrate", 0.0, "Anteil der Anfragen, die mit 503 fehlschlagen (0.0–1.0)")
	format := flag.String("format", provider.DefaultFormat, "Antwortformat: "+strings.Join(provider.FormatNames(), ", "))
	flag.Parse()

	if *name == "" || *delay < 0 || *failRate < 0 || *failRate > 1 {
		fmt.Fprintln(os.Stderr, "ungültige Flags: -name darf nicht leer sein, -delay ≥ 0, -failrate zwischen 0 und 1")
		os.Exit(2)
	}
	if !slices.Contains(provider.FormatNames(), *format) {
		fmt.Fprintf(os.Stderr, "unbekanntes -format %q (verfügbar: %s)\n", *format, strings.Join(provider.FormatNames(), ", "))
		os.Exit(2)
	}

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           newHandler(*name, *delay, *failRate, *format),
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("mock-anbieter startet", "name", *name, "addr", addr, "delay", *delay, "failrate", *failRate, "format", *format)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func newHandler(name string, delay time.Duration, failRate float64, format string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /flights", func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(time.Duration(rand.Int63n(int64(delay) + 1)))
		}
		if rand.Float64() < failRate {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		q := r.URL.Query()
		date, err := time.Parse(time.DateOnly, q.Get("date"))
		if err != nil {
			http.Error(w, "bad date", http.StatusBadRequest)
			return
		}
		offers := generate(name, strings.ToUpper(q.Get("from")), strings.ToUpper(q.Get("to")), date)
		body, err := render(format, offers)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	return mux
}

// generate erzeugt für dieselbe Anfrage immer dieselben Flüge (Seed aus den Parametern).
// Die Preise sind in EUR; render rechnet sie bei Bedarf in die Anbieterwährung um.
func generate(provider, from, to string, date time.Time) []flight.Offer {
	h := fnv.New64a()
	fmt.Fprint(h, provider, from, to, date.Format(time.DateOnly))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	prefix := strings.ToUpper(provider[len(provider)-1:]) + "X" // immer 2 Zeichen, siehe render "nested"
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

// Umrechnung EUR → Anbieterwährung, passend zu den Beispielkursen in internal/currency.
func eurToUSD(cents int) int { return (cents*100 + 46) / 92 }
func eurToGBP(cents int) int { return (cents*100 + 58) / 117 }

var berlin = mustLoadLocation("Europe/Berlin")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// render bringt die Angebote in das JSON-Format des jeweiligen Anbieters.
func render(format string, offers []flight.Offer) (any, error) {
	switch format {
	case "standard":
		return offers, nil

	case "skyjson":
		type skyFlight struct {
			FlightNo       string `json:"flightNo"`
			Route          string `json:"route"`
			DepartureLocal string `json:"departureLocal"`
			ArrivalLocal   string `json:"arrivalLocal"`
			TimeZone       string `json:"timeZone"`
			Price          string `json:"price"`
			Currency       string `json:"currency"`
		}
		out := make([]skyFlight, 0, len(offers))
		for _, o := range offers {
			usd := eurToUSD(o.PriceCents)
			out = append(out, skyFlight{
				FlightNo:       o.FlightNumber,
				Route:          o.From + "-" + o.To,
				DepartureLocal: o.Departure.In(berlin).Format(provider.SkyTimeLayout),
				ArrivalLocal:   o.Arrival.In(berlin).Format(provider.SkyTimeLayout),
				TimeZone:       berlin.String(),
				Price:          fmt.Sprintf("%d.%02d", usd/100, usd%100),
				Currency:       "USD",
			})
		}
		return out, nil

	case "nested":
		type segment struct {
			From string `json:"from"`
			To   string `json:"to"`
			Dep  int64  `json:"dep"`
			Arr  int64  `json:"arr"`
		}
		type fare struct {
			TotalMinor int `json:"total_minor"`
		}
		type nestedFlight struct {
			Carrier string  `json:"carrier"`
			Number  int     `json:"number"`
			Segment segment `json:"segment"`
			Fare    fare    `json:"fare"`
		}
		flights := make([]nestedFlight, 0, len(offers))
		for _, o := range offers {
			num, err := strconv.Atoi(o.FlightNumber[2:])
			if err != nil {
				return nil, fmt.Errorf("flugnummer %q: %w", o.FlightNumber, err)
			}
			flights = append(flights, nestedFlight{
				Carrier: o.FlightNumber[:2],
				Number:  num,
				Segment: segment{From: o.From, To: o.To, Dep: o.Departure.Unix(), Arr: o.Arrival.Unix()},
				Fare:    fare{TotalMinor: eurToGBP(o.PriceCents)},
			})
		}
		type data struct {
			Currency string         `json:"currency"`
			Flights  []nestedFlight `json:"flights"`
		}
		return map[string]data{"data": {Currency: "GBP", Flights: flights}}, nil
	}
	return nil, fmt.Errorf("unbekanntes format %q", format)
}

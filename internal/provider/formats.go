package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"flugsuche-go/internal/flight"
)

// Decoder übersetzt die Antwort eines Anbieters in Angebote.
//
// Preise bleiben in der Originalwährung; Plausibilitätsprüfung und Umrechnung
// in EUR erledigt HTTPProvider für alle Formate gemeinsam. Passt ein Datensatz
// nicht ins Format, lehnt der Decoder die ganze Antwort ab.
type Decoder func(r io.Reader) ([]flight.Offer, error)

// DefaultFormat wird benutzt, wenn für einen Anbieter kein Format angegeben ist.
const DefaultFormat = "standard"

var formats = map[string]Decoder{
	"standard": decodeStandard,
	"skyjson":  decodeSkyJSON,
	"nested":   decodeNested,
}

// FormatByName liefert den Decoder für ein Antwortformat.
func FormatByName(name string) (Decoder, error) {
	d, ok := formats[name]
	if !ok {
		return nil, fmt.Errorf("unbekanntes format %q (verfügbar: %s)", name, strings.Join(FormatNames(), ", "))
	}
	return d, nil
}

// FormatNames liefert alle bekannten Formate, alphabetisch sortiert.
func FormatNames() []string {
	names := make([]string, 0, len(formats))
	for n := range formats {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------------------
// standard: das eigene Domänenformat (flight.Offer als JSON-Array).
//
//	[{"flight_number":"AX338","from":"LEJ","to":"PMI",
//	  "departure":"2026-12-20T12:00:00Z","arrival":"2026-12-20T15:23:00Z",
//	  "price_cents":6300,"currency":"EUR"}]

func decodeStandard(r io.Reader) ([]flight.Offer, error) {
	var offers []flight.Offer
	if err := json.NewDecoder(r).Decode(&offers); err != nil {
		return nil, err
	}
	return offers, nil
}

// ---------------------------------------------------------------------------
// skyjson: camelCase, Strecke als ein Feld, Ortszeit + IANA-Zeitzone,
// Preis als Dezimal-String.
//
//	[{"flightNo":"BX123","route":"LEJ-PMI",
//	  "departureLocal":"2026-12-20 08:15","arrivalLocal":"2026-12-20 10:40",
//	  "timeZone":"Europe/Berlin","price":"59.90","currency":"USD"}]

// SkyTimeLayout ist das Zeitformat von skyjson (Ortszeit ohne Offset).
const SkyTimeLayout = "2006-01-02 15:04"

type skyFlight struct {
	FlightNo       string `json:"flightNo"`
	Route          string `json:"route"`
	DepartureLocal string `json:"departureLocal"`
	ArrivalLocal   string `json:"arrivalLocal"`
	TimeZone       string `json:"timeZone"`
	Price          string `json:"price"`
	Currency       string `json:"currency"`
}

func decodeSkyJSON(r io.Reader) ([]flight.Offer, error) {
	var raw []skyFlight
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	offers := make([]flight.Offer, 0, len(raw))
	for i, f := range raw {
		o, err := f.toOffer()
		if err != nil {
			return nil, fmt.Errorf("flug %d: %w", i, err)
		}
		offers = append(offers, o)
	}
	return offers, nil
}

func (f skyFlight) toOffer() (flight.Offer, error) {
	from, to, ok := strings.Cut(f.Route, "-")
	if !ok {
		return flight.Offer{}, fmt.Errorf("route %q: erwarte FROM-TO", f.Route)
	}
	// LoadLocation("") liefert UTC und "Local" die Serverzeit – beides wäre still falsch.
	if f.TimeZone == "" || f.TimeZone == "Local" {
		return flight.Offer{}, fmt.Errorf("zeitzone %q ungültig", f.TimeZone)
	}
	loc, err := time.LoadLocation(f.TimeZone)
	if err != nil {
		return flight.Offer{}, fmt.Errorf("zeitzone %q: %w", f.TimeZone, err)
	}
	dep, err := time.ParseInLocation(SkyTimeLayout, f.DepartureLocal, loc)
	if err != nil {
		return flight.Offer{}, fmt.Errorf("abflug %q: %w", f.DepartureLocal, err)
	}
	arr, err := time.ParseInLocation(SkyTimeLayout, f.ArrivalLocal, loc)
	if err != nil {
		return flight.Offer{}, fmt.Errorf("ankunft %q: %w", f.ArrivalLocal, err)
	}
	price, err := parseDecimalPrice(f.Price)
	if err != nil {
		return flight.Offer{}, err
	}
	return flight.Offer{
		FlightNumber: f.FlightNo,
		From:         from,
		To:           to,
		Departure:    dep.UTC(),
		Arrival:      arr.UTC(),
		PriceCents:   price,
		Currency:     f.Currency,
	}, nil
}

// parseDecimalPrice wandelt "59", "59.9" oder "59.90" in Cent um – ohne float.
func parseDecimalPrice(s string) (int, error) {
	whole, frac, hasFrac := strings.Cut(s, ".")
	if !isDigits(whole) || (hasFrac && (len(frac) == 0 || len(frac) > 2 || !isDigits(frac))) {
		return 0, fmt.Errorf("ungültiger preis %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.Atoi(whole + frac)
	if err != nil { // Überlauf
		return 0, fmt.Errorf("ungültiger preis %q", s)
	}
	return n, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// nested: Umschlag-Objekt, Währung einmal für alle Flüge, Unix-Zeitstempel,
// Flugnummer aufgeteilt in Airline-Code und Nummer, Preis in kleinster Einheit.
//
//	{"data":{"currency":"GBP","flights":[{"carrier":"CX","number":951,
//	  "segment":{"from":"LEJ","to":"PMI","dep":1797753600,"arr":1797762000},
//	  "fare":{"total_minor":4700}}]}}

type nestedResponse struct {
	Data *struct {
		Currency string         `json:"currency"`
		Flights  []nestedFlight `json:"flights"`
	} `json:"data"`
}

type nestedFlight struct {
	Carrier string `json:"carrier"`
	Number  int    `json:"number"`
	Segment struct {
		From string `json:"from"`
		To   string `json:"to"`
		Dep  int64  `json:"dep"`
		Arr  int64  `json:"arr"`
	} `json:"segment"`
	Fare struct {
		TotalMinor int `json:"total_minor"`
	} `json:"fare"`
}

func decodeNested(r io.Reader) ([]flight.Offer, error) {
	var resp nestedResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, errors.New(`feld "data" fehlt`)
	}
	offers := make([]flight.Offer, 0, len(resp.Data.Flights))
	for i, f := range resp.Data.Flights {
		if f.Carrier == "" || f.Number <= 0 {
			return nil, fmt.Errorf("flug %d: carrier oder number fehlt", i)
		}
		if f.Segment.Dep == 0 || f.Segment.Arr == 0 {
			return nil, fmt.Errorf("flug %d: zeiten fehlen", i)
		}
		offers = append(offers, flight.Offer{
			FlightNumber: fmt.Sprintf("%s%d", f.Carrier, f.Number),
			From:         f.Segment.From,
			To:           f.Segment.To,
			Departure:    time.Unix(f.Segment.Dep, 0).UTC(),
			Arrival:      time.Unix(f.Segment.Arr, 0).UTC(),
			PriceCents:   f.Fare.TotalMinor,
			Currency:     resp.Data.Currency,
		})
	}
	return offers, nil
}

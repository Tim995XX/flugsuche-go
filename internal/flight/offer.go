// Package flight enthält die Domänen-Modelle der Flugsuche.
package flight

import "time"

// Query beschreibt eine Suchanfrage.
type Query struct {
	From string    // IATA-Code, z. B. "LEJ"
	To   string    // IATA-Code, z. B. "PMI"
	Date time.Time // Abflugdatum (nur der Tag zählt)
}

// Offer ist ein einzelnes Flugangebot eines Anbieters.
type Offer struct {
	Provider     string    `json:"provider"`
	FlightNumber string    `json:"flight_number"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Departure    time.Time `json:"departure"`
	Arrival      time.Time `json:"arrival"`
	PriceCents   int       `json:"price_cents"` // Geldbeträge nie als float!
	Currency     string    `json:"currency"`
}

// Duration liefert die Flugdauer.
func (o Offer) Duration() time.Duration {
	return o.Arrival.Sub(o.Departure)
}

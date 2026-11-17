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
	PriceCents   int       `json:"price_cents"` // Geldbeträge nie als float! Nach der Normalisierung immer EUR.
	Currency     string    `json:"currency"`
	Original     *Price    `json:"original_price,omitempty"` // gesetzt, wenn umgerechnet wurde
}

// Price ist ein Geldbetrag in der kleinsten Einheit der Währung (Cent, Pence, …).
type Price struct {
	AmountCents int    `json:"amount_cents"`
	Currency    string `json:"currency"`
}

// Duration liefert die Flugdauer.
func (o Offer) Duration() time.Duration {
	return o.Arrival.Sub(o.Departure)
}

// Package provider kapselt den Zugriff auf externe Flug-Anbieter.
//
// Jeder Anbieter hat sein eigenes JSON-Format. HTTPProvider übernimmt alles,
// was für alle gleich ist (HTTP, Statuscodes, Größenlimit, Plausibilitätsprüfung,
// Umrechnung in EUR); pro Format gibt es nur einen kleinen Decoder (formats.go).
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flugsuche-go/internal/currency"
	"flugsuche-go/internal/flight"
)

// Provider ist die Schnittstelle, die jeder Anbieter erfüllen muss.
// Ein neuer Anbieter = ein neuer Typ, der dieses Interface implementiert.
type Provider interface {
	Name() string
	Search(ctx context.Context, q flight.Query) ([]flight.Offer, error)
}

// maxResponseBytes begrenzt die Antwortgröße, damit ein fehlerhafter
// Anbieter den Server nicht mit riesigen Antworten lahmlegen kann.
const maxResponseBytes = 1 << 20 // 1 MiB

// maxPriceCentsEUR ist der höchste plausible Preis eines Fluges (100.000 €).
// Alles darüber ist mit hoher Wahrscheinlichkeit ein Fehler des Anbieters.
const maxPriceCentsEUR = 100_000_00

// Config beschreibt einen HTTP-Anbieter. Nur Name und BaseURL sind Pflicht.
type Config struct {
	Name      string
	BaseURL   string
	Decoder   Decoder            // Standard: Format "standard"
	Converter currency.Converter // Standard: currency.Default
	Client    *http.Client       // Standard: Client mit 5 s Timeout
}

// HTTPProvider fragt einen Anbieter über eine JSON-HTTP-API ab.
type HTTPProvider struct {
	name    string
	baseURL string
	decode  Decoder
	conv    currency.Converter
	client  *http.Client
}

// NewHTTP erzeugt einen HTTP-basierten Anbieter.
func NewHTTP(cfg Config) *HTTPProvider {
	p := &HTTPProvider{
		name:    cfg.Name,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		decode:  cfg.Decoder,
		conv:    cfg.Converter,
		client:  cfg.Client,
	}
	if p.decode == nil {
		p.decode = decodeStandard
	}
	if p.conv == nil {
		p.conv = currency.Default
	}
	if p.client == nil {
		p.client = &http.Client{Timeout: 5 * time.Second}
	}
	return p
}

func (p *HTTPProvider) Name() string { return p.name }

func (p *HTTPProvider) Search(ctx context.Context, q flight.Query) ([]flight.Offer, error) {
	params := url.Values{}
	params.Set("from", q.From)
	params.Set("to", q.To)
	params.Set("date", q.Date.Format(time.DateOnly))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/flights?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("%s: request bauen: %w", p.name, err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: anfrage fehlgeschlagen: %w", p.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unerwarteter status %d", p.name, resp.StatusCode)
	}

	offers, err := p.decode(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%s: antwort dekodieren: %w", p.name, err)
	}
	for i := range offers {
		if err := p.normalize(&offers[i], q); err != nil {
			return nil, fmt.Errorf("%s: angebot %d (%q): %w", p.name, i, offers[i].FlightNumber, err)
		}
	}
	return offers, nil
}

// normalize prüft ein Angebot auf Plausibilität und rechnet den Preis in EUR um.
// Lieber ein Anbieter-Fehler als ein falsches Angebot ganz oben in der Liste.
func (p *HTTPProvider) normalize(o *flight.Offer, q flight.Query) error {
	o.Provider = p.name // vom Anbieter gemeldete Namen sind nicht vertrauenswürdig
	if o.From == "" {
		o.From = q.From
	}
	if o.To == "" {
		o.To = q.To
	}
	o.From, o.To = strings.ToUpper(o.From), strings.ToUpper(o.To)
	o.Currency = strings.ToUpper(o.Currency)

	switch {
	case o.FlightNumber == "":
		return errors.New("flugnummer fehlt")
	case o.From != q.From || o.To != q.To:
		return fmt.Errorf("falsche strecke %s-%s", o.From, o.To)
	case o.Departure.IsZero():
		return errors.New("abflugzeit fehlt")
	case !o.Arrival.After(o.Departure):
		return errors.New("ankunft liegt nicht nach dem abflug")
	case o.PriceCents <= 0:
		return fmt.Errorf("ungültiger preis %d", o.PriceCents)
	case o.Currency == "":
		return errors.New("währung fehlt")
	}

	o.Original = nil
	if o.Currency != currency.EUR {
		eur, err := p.conv.ToEUR(o.PriceCents, o.Currency)
		if err != nil {
			return err
		}
		o.Original = &flight.Price{AmountCents: o.PriceCents, Currency: o.Currency}
		o.PriceCents, o.Currency = eur, currency.EUR
	}
	if o.PriceCents > maxPriceCentsEUR {
		return fmt.Errorf("unplausibler preis %d EUR-Cent (max. %d)", o.PriceCents, maxPriceCentsEUR)
	}
	return nil
}

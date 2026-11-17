// Package provider kapselt den Zugriff auf externe Flug-Anbieter.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// HTTPProvider fragt einen Anbieter über eine JSON-HTTP-API ab.
type HTTPProvider struct {
	name    string
	baseURL string
	client  *http.Client
}

// NewHTTP erzeugt einen HTTP-basierten Anbieter.
func NewHTTP(name, baseURL string, client *http.Client) *HTTPProvider {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPProvider{name: name, baseURL: strings.TrimRight(baseURL, "/"), client: client}
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

	var offers []flight.Offer
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&offers); err != nil {
		return nil, fmt.Errorf("%s: antwort dekodieren: %w", p.name, err)
	}
	for i := range offers {
		offers[i].Provider = p.name
	}
	return offers, nil
}

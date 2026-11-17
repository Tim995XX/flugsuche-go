package provider

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"flugsuche-go/internal/flight"
)

func decode(t *testing.T, format, body string) ([]flight.Offer, error) {
	t.Helper()
	d, err := FormatByName(format)
	if err != nil {
		t.Fatal(err)
	}
	return d(strings.NewReader(body))
}

func TestDecodeFormats(t *testing.T) {
	// 20.12.2026 08:15 in Berlin (Winterzeit, UTC+1) = 07:15 UTC
	dep := time.Date(2026, 12, 20, 7, 15, 0, 0, time.UTC)
	arr := time.Date(2026, 12, 20, 9, 40, 0, 0, time.UTC)

	tests := []struct {
		format string
		body   string
		want   flight.Offer
	}{
		{
			format: "standard",
			body: `[{"flight_number":"AX1","from":"LEJ","to":"PMI",
				"departure":"2026-12-20T07:15:00Z","arrival":"2026-12-20T09:40:00Z",
				"price_cents":5990,"currency":"EUR"}]`,
			want: flight.Offer{FlightNumber: "AX1", From: "LEJ", To: "PMI", Departure: dep, Arrival: arr, PriceCents: 5990, Currency: "EUR"},
		},
		{
			format: "skyjson",
			body: `[{"flightNo":"BX1","route":"LEJ-PMI",
				"departureLocal":"2026-12-20 08:15","arrivalLocal":"2026-12-20 10:40",
				"timeZone":"Europe/Berlin","price":"59.9","currency":"USD"}]`,
			want: flight.Offer{FlightNumber: "BX1", From: "LEJ", To: "PMI", Departure: dep, Arrival: arr, PriceCents: 5990, Currency: "USD"},
		},
		{
			format: "nested",
			body: `{"data":{"currency":"GBP","flights":[{"carrier":"CX","number":951,
				"segment":{"from":"LEJ","to":"PMI","dep":` + unix(dep) + `,"arr":` + unix(arr) + `},
				"fare":{"total_minor":5990}}]}}`,
			want: flight.Offer{FlightNumber: "CX951", From: "LEJ", To: "PMI", Departure: dep, Arrival: arr, PriceCents: 5990, Currency: "GBP"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			got, err := decode(t, tt.format, tt.body)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			g := got[0]
			if g.FlightNumber != tt.want.FlightNumber || g.From != tt.want.From || g.To != tt.want.To ||
				!g.Departure.Equal(tt.want.Departure) || !g.Arrival.Equal(tt.want.Arrival) ||
				g.PriceCents != tt.want.PriceCents || g.Currency != tt.want.Currency {
				t.Errorf("got  %+v\nwant %+v", g, tt.want)
			}
		})
	}
}

func TestSkyJSONSummerTime(t *testing.T) {
	// Im Juli gilt in Berlin UTC+2.
	got, err := decode(t, "skyjson", `[{"flightNo":"BX1","route":"LEJ-PMI",
		"departureLocal":"2026-07-01 08:00","arrivalLocal":"2026-07-01 10:00",
		"timeZone":"Europe/Berlin","price":"10","currency":"USD"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC); !got[0].Departure.Equal(want) {
		t.Errorf("Departure = %v, want %v", got[0].Departure, want)
	}
}

func TestDecodeErrors(t *testing.T) {
	sky := func(field, value string) string {
		f := map[string]string{
			"flightNo": `"BX1"`, "route": `"LEJ-PMI"`,
			"departureLocal": `"2026-12-20 08:15"`, "arrivalLocal": `"2026-12-20 10:40"`,
			"timeZone": `"Europe/Berlin"`, "price": `"59.90"`, "currency": `"USD"`,
		}
		f[field] = value
		var parts []string
		for k, v := range f {
			parts = append(parts, `"`+k+`":`+v)
		}
		return "[{" + strings.Join(parts, ",") + "}]"
	}

	tests := []struct {
		name, format, body, wantErr string
	}{
		{"standard kein array", "standard", `{"flight_number":"AX1"}`, "cannot unmarshal"},
		{"sky route", "skyjson", sky("route", `"LEJPMI"`), "route"},
		{"sky zeitzone leer", "skyjson", sky("timeZone", `""`), "zeitzone"},
		{"sky zeitzone local", "skyjson", sky("timeZone", `"Local"`), "zeitzone"},
		{"sky zeitzone unbekannt", "skyjson", sky("timeZone", `"Mars/Olympus"`), "zeitzone"},
		{"sky abflug format", "skyjson", sky("departureLocal", `"20.12.2026 08:15"`), "abflug"},
		{"sky ankunft fehlt", "skyjson", sky("arrivalLocal", `""`), "ankunft"},
		{"sky preis komma", "skyjson", sky("price", `"59,90"`), "ungültiger preis"},
		{"sky preis als zahl", "skyjson", sky("price", `59.90`), "cannot unmarshal"},
		{"nested ohne data", "nested", `{"flights":[]}`, `"data" fehlt`},
		{"nested ohne carrier", "nested", `{"data":{"currency":"GBP","flights":[{"number":1,"segment":{"dep":1,"arr":2}}]}}`, "carrier"},
		{"nested ohne zeiten", "nested", `{"data":{"currency":"GBP","flights":[{"carrier":"CX","number":1}]}}`, "zeiten"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decode(t, tt.format, tt.body)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want enthält %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseDecimalPrice(t *testing.T) {
	ok := map[string]int{"59": 5900, "59.9": 5990, "59.90": 5990, "0.05": 5, "1234.56": 123456}
	for in, want := range ok {
		if got, err := parseDecimalPrice(in); err != nil || got != want {
			t.Errorf("parseDecimalPrice(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".5", "59.", "59.123", "-5", "+5", "5e2", "59,90", " 59", "99999999999999999999"} {
		if _, err := parseDecimalPrice(in); err == nil {
			t.Errorf("parseDecimalPrice(%q): Fehler erwartet", in)
		}
	}
}

func TestFormatByNameUnknown(t *testing.T) {
	_, err := FormatByName("xml")
	if err == nil || !strings.Contains(err.Error(), "nested, skyjson, standard") {
		t.Errorf("err = %v, sollte verfügbare Formate auflisten", err)
	}
}

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

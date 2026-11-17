package currency

import (
	"errors"
	"testing"
)

func TestFixedRatesToEUR(t *testing.T) {
	rates := FixedRates{EUR: 1_000_000, "USD": 920_000, "GBP": 1_170_000}

	tests := []struct {
		amount   int
		currency string
		want     int
	}{
		{5900, EUR, 5900},
		{10000, "USD", 9200},
		{5900, "USD", 5428}, // 54,28 exakt
		{1, "USD", 1},       // 0,92 Cent → 1 Cent (aufgerundet)
		{1, "GBP", 1},       // 1,17 Cent → 1 Cent (abgerundet)
		{50, "USD", 46},
		{0, "USD", 0},
		{4700, "GBP", 5499}, // 54,99
	}
	for _, tt := range tests {
		got, err := rates.ToEUR(tt.amount, tt.currency)
		if err != nil {
			t.Fatalf("ToEUR(%d, %s): %v", tt.amount, tt.currency, err)
		}
		if got != tt.want {
			t.Errorf("ToEUR(%d, %s) = %d, want %d", tt.amount, tt.currency, got, tt.want)
		}
	}
}

func TestFixedRatesErrors(t *testing.T) {
	if _, err := Default.ToEUR(100, "XYZ"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unbekannte Währung: err = %v, want ErrUnknown", err)
	}
	if _, err := Default.ToEUR(-1, EUR); err == nil {
		t.Error("negativer Betrag sollte Fehler liefern")
	}
}

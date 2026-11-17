// Package currency rechnet Preise in Euro um.
//
// Alle Beträge sind Ganzzahlen in der kleinsten Einheit (Cent, Pence, …),
// Kurse werden als Mikro-Euro pro Einheit gespeichert – kein float64 im Spiel.
package currency

import (
	"errors"
	"fmt"
)

// EUR ist die Zielwährung aller Preise im System.
const EUR = "EUR"

// ErrUnknown wird geliefert, wenn für eine Währung kein Kurs bekannt ist.
var ErrUnknown = errors.New("unbekannte währung")

// Converter rechnet einen Betrag (kleinste Einheit) in Euro-Cent um.
// Das Interface erlaubt später, echte Tageskurse anzubinden.
type Converter interface {
	ToEUR(amountCents int, currency string) (int, error)
}

// FixedRates ist eine feste Kurstabelle: Mikro-Euro pro Einheit der Währung.
// Beispiel: 1 USD = 0,92 EUR → 920_000.
type FixedRates map[string]int64

// Default enthält Beispielkurse (nicht tagesaktuell!) für die Mock-Anbieter.
var Default = FixedRates{
	EUR:   1_000_000,
	"USD": 920_000,
	"GBP": 1_170_000,
	"CHF": 1_060_000,
}

// ToEUR rechnet um und rundet kaufmännisch auf ganze Cent.
func (r FixedRates) ToEUR(amountCents int, currency string) (int, error) {
	rate, ok := r[currency]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknown, currency)
	}
	if amountCents < 0 {
		return 0, fmt.Errorf("negativer betrag %d", amountCents)
	}
	return int((int64(amountCents)*rate + 500_000) / 1_000_000), nil
}

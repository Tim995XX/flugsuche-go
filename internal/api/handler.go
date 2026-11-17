// Package api stellt die HTTP-Schnittstelle bereit.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"flugsuche-go/internal/flight"
	"flugsuche-go/internal/search"
)

var iata = regexp.MustCompile(`^[A-Z]{3}$`)

// maxDaysAhead begrenzt, wie weit in der Zukunft gesucht werden darf
// (Airlines verkaufen meist höchstens ~1 Jahr im Voraus).
const maxDaysAhead = 365

// now ist austauschbar, damit Tests nicht vom echten Datum abhängen.
var now = time.Now

// NewHandler baut den HTTP-Router.
func NewHandler(svc *search.Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q, msg := parseQuery(r)
		if msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}

		start := time.Now()
		res := svc.Search(r.Context(), q)
		slog.Info("suche",
			"from", q.From, "to", q.To, "date", q.Date.Format(time.DateOnly),
			"offers", len(res.Offers), "provider_errors", len(res.Errors),
			"duration", time.Since(start))

		writeJSON(w, http.StatusOK, res)
	})

	return mux
}

func parseQuery(r *http.Request) (flight.Query, string) {
	v := r.URL.Query()
	from := strings.ToUpper(v.Get("from"))
	to := strings.ToUpper(v.Get("to"))

	if !iata.MatchString(from) || !iata.MatchString(to) {
		return flight.Query{}, "from und to müssen 3-stellige IATA-Codes sein (z. B. LEJ)"
	}
	if from == to {
		return flight.Query{}, "from und to dürfen nicht gleich sein"
	}
	date, err := time.Parse(time.DateOnly, v.Get("date"))
	if err != nil {
		return flight.Query{}, "date muss im Format YYYY-MM-DD sein"
	}
	today := now().UTC().Truncate(24 * time.Hour)
	if date.Before(today) {
		return flight.Query{}, "date darf nicht in der Vergangenheit liegen"
	}
	if date.After(today.AddDate(0, 0, maxDaysAhead)) {
		return flight.Query{}, "date darf höchstens 365 Tage in der Zukunft liegen"
	}
	return flight.Query{From: from, To: to, Date: date}, ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("json schreiben", "err", err)
	}
}

# flugsuche-go

Ein kleiner Flugpreis-Vergleich in Go: Das Backend fragt mehrere Airline-Anbieter **parallel** ab, verwirft langsame oder fehlerhafte Antworten nach einem Timeout, entfernt Duplikate und liefert die günstigsten Angebote zuerst.

## Architektur

```
                 ┌──────────────┐   parallel, mit Timeout   ┌─────────────┐
 GET /search ──▶ │  api.Handler │──▶ search.Service ──┬────▶│ airline-a   │
                 └──────────────┘                     ├────▶│ airline-b   │
                                                      └────▶│ airline-c   │
                                                            └─────────────┘
                     ◀── zusammengeführt, dedupliziert, nach Preis sortiert
```

| Paket | Aufgabe |
|---|---|
| `cmd/api` | Startet den HTTP-Server (Konfiguration, Graceful Shutdown) |
| `cmd/mockprovider` | Simulierter Anbieter mit einstellbarer Latenz und Fehlerrate |
| `internal/api` | HTTP-Routing, Validierung der Eingaben |
| `internal/search` | Paralleles Abfragen, Timeout, Deduplizierung, Sortierung |
| `internal/provider` | `Provider`-Interface + HTTP-Implementierung |
| `internal/flight` | Domänen-Modelle (`Query`, `Offer`) |

## Starten

Voraussetzung: Go ≥ 1.22.

```bash
make mocks     # startet drei Mock-Anbieter auf :9001–9003
make run       # startet die API auf :8080

curl "localhost:8080/search?from=LEJ&to=PMI&date=2026-12-20"
```

`airline-c` antwortet absichtlich langsam (bis 1,2 s) und landet daher oft in `errors` – das zeigt, dass ein langsamer Anbieter die Suche nicht blockiert.

Tests:

```bash
make test-race
```

## Entscheidungen

- **Preise als Cent-Integer**, nicht als `float64` → keine Rundungsfehler.
- **Ein Gesamt-Timeout pro Suche** statt Retries: Der Nutzer wartet lieber 800 ms auf 2 von 3 Anbietern als 3 s auf alle.
- **Nur Standardbibliothek**: Router (`net/http` ab Go 1.22), Logging (`log/slog`). Weniger Abhängigkeiten, leichter zu verstehen.
- **Interface für Anbieter**: Ein neuer Anbieter mit anderem API-Format braucht nur einen neuen Typ – Service und Handler bleiben unverändert.

## Roadmap

- [x] Schritt 1: API-Endpunkt, Mock-Anbieter, paralleles Abfragen, Tests
- [ ] Schritt 2: Anbieter mit **unterschiedlichen** JSON-Formaten (Adapter pro Anbieter)
- [ ] Schritt 3: Caching (erst In-Memory mit TTL, dann Redis), `Dockerfile` + `docker-compose.yml`
- [ ] Schritt 4: GitHub Actions (`golangci-lint`, `go test -race`, Docker-Build)
- [ ] Schritt 5: Prometheus-Metriken (`/metrics`: Latenz pro Anbieter, Cache-Hit-Rate) + Grafana-Dashboard
- [ ] Schritt 6: Lasttest mit `k6`, Ergebnisse hier dokumentieren

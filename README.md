# flugsuche-go

Ein kleiner Flugpreis-Vergleich in Go: Das Backend fragt mehrere Airline-Anbieter **parallel** ab, übersetzt deren **unterschiedliche JSON-Formate** in ein gemeinsames Modell, rechnet alle Preise in **EUR** um, verwirft langsame oder fehlerhafte Antworten nach einem Timeout, entfernt Duplikate und liefert die günstigsten Angebote zuerst.

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
| `cmd/mockprovider` | Simulierter Anbieter mit einstellbarer Latenz, Fehlerrate und Antwortformat |
| `internal/api` | HTTP-Routing, Validierung der Eingaben |
| `internal/search` | Paralleles Abfragen, Timeout, Deduplizierung, Sortierung |
| `internal/provider` | `Provider`-Interface, HTTP-Implementierung, ein Decoder pro Antwortformat |
| `internal/currency` | Umrechnung in EUR (feste Kurstabelle hinter einem Interface) |
| `internal/flight` | Domänen-Modelle (`Query`, `Offer`) |

## Starten

Voraussetzung: Go ≥ 1.22.

```bash
make mocks     # startet drei Mock-Anbieter auf :9001–9003
make run       # startet die API auf :8080

curl "localhost:8080/search?from=LEJ&to=PMI&date=2026-12-20"
```

Die Mock-Anbieter sprechen absichtlich verschiedene Formate (siehe unten). `airline-c` antwortet außerdem absichtlich langsam (bis 1,2 s) und landet daher oft in `errors` – das zeigt, dass ein langsamer Anbieter die Suche nicht blockiert.

## Anbieter und Formate

Konfiguriert über die Umgebungsvariable `PROVIDERS`:

```
PROVIDERS="airline-a=http://localhost:9001,airline-b=http://localhost:9002|format=skyjson,airline-c=http://localhost:9003|format=nested"
```

| Format | Merkmale | Beispiel |
|---|---|---|
| `standard` (Default) | eigenes Domänenformat, Preis in Cent | `[{"flight_number":"AX338","departure":"2026-12-20T12:00:00Z","price_cents":6300,"currency":"EUR",…}]` |
| `skyjson` | camelCase, Strecke als `"LEJ-PMI"`, Ortszeit + IANA-Zeitzone, Preis als Dezimal-String | `[{"flightNo":"BX975","route":"LEJ-PMI","departureLocal":"2026-12-20 14:15","timeZone":"Europe/Berlin","price":"292.39","currency":"USD"}]` |
| `nested` | Umschlag `data`, Währung einmal pro Antwort, Unix-Zeitstempel, Flugnummer geteilt | `{"data":{"currency":"GBP","flights":[{"carrier":"CX","number":951,"segment":{"dep":1797801300,…},"fare":{"total_minor":4701}}]}}` |

Alle Preise werden in EUR umgerechnet; der Originalpreis bleibt als `original_price` im Angebot:

```json
{"provider":"airline-c","flight_number":"CX951","price_cents":5500,"currency":"EUR",
 "original_price":{"amount_cents":4701,"currency":"GBP"}, …}
```

**Neuen Anbieter anbinden:** eine `decodeXyz(io.Reader) ([]flight.Offer, error)` in `internal/provider/formats.go` schreiben und in `formats` eintragen – HTTP, Validierung und Umrechnung kommen gratis dazu. Der Test in `cmd/mockprovider` prüft automatisch, dass jedes Format vom Mock erzeugt und vom Adapter korrekt gelesen wird.

Tests:

```bash
make test-race
```

## Entscheidungen

- **Preise als Cent-Integer**, nicht als `float64` → keine Rundungsfehler.
- **Ein Gesamt-Timeout pro Suche** statt Retries: Der Nutzer wartet lieber 800 ms auf 2 von 3 Anbietern als 3 s auf alle.
- **Nur Standardbibliothek**: Router (`net/http` ab Go 1.22), Logging (`log/slog`). Weniger Abhängigkeiten, leichter zu verstehen.
- **Interface für Anbieter**: Ein neuer Anbieter mit anderem API-Format braucht nur einen neuen Decoder – Service und Handler bleiben unverändert.
- **Decoder übersetzen nur, `HTTPProvider` prüft**: Plausibilität (Strecke, Zeiten, Preis > 0) und Umrechnung passieren an *einer* Stelle für alle Formate.
- **Lieber Fehler als falsches Angebot**: Ist ein Datensatz kaputt oder die Währung unbekannt, landet der ganze Anbieter in `errors`, statt dass ein falscher Preis oben in der Liste steht.
- **Alles in EUR, Kurse als Ganzzahlen** (Mikro-Euro pro Einheit), kaufmännisch gerundet. Die Kurse in `currency.Default` sind Beispielwerte, nicht tagesaktuell – das `Converter`-Interface erlaubt später eine echte Kursquelle.
- **Zeitzonen eingebettet** (`time/tzdata`): funktioniert auch in minimalen Docker-Images ohne `/usr/share/zoneinfo`.

## Roadmap

- [x] Schritt 1: API-Endpunkt, Mock-Anbieter, paralleles Abfragen, Tests
- [x] Schritt 2: Anbieter mit **unterschiedlichen** JSON-Formaten (Adapter pro Anbieter), Umrechnung in EUR
- [ ] Schritt 3: Caching (erst In-Memory mit TTL, dann Redis), `Dockerfile` + `docker-compose.yml`
- [ ] Schritt 4: GitHub Actions (`golangci-lint`, `go test -race`, Docker-Build)
- [ ] Schritt 5: Prometheus-Metriken (`/metrics`: Latenz pro Anbieter, Cache-Hit-Rate) + Grafana-Dashboard
- [ ] Schritt 6: Lasttest mit `k6`, Ergebnisse hier dokumentieren

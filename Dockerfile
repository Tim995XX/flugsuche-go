# syntax=docker/dockerfile:1

# --- Build ------------------------------------------------------------------
FROM golang:1.27.4-alpine AS build
WORKDIR /src

# Abhängigkeiten zuerst: Diese Schicht bleibt gecacht, solange go.mod/go.sum gleich sind.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Statisch gelinkt (CGO aus), ohne Debug-Infos → läuft im leeren distroless-Image.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

# --- Laufzeit ---------------------------------------------------------------
# Kein Shell, kein Paketmanager, läuft als nicht-root-Benutzer.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /out/mockprovider /app/

ENV ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/app/api", "healthcheck"]
ENTRYPOINT ["/app/api"]

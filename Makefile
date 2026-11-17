.PHONY: run build mocks stop-mocks test test-race cover fmt vet lint docker-build up down

# Drei Mock-Anbieter im Hintergrund starten (beenden mit: pkill -f mockprovider)
mocks:
	go run ./cmd/mockprovider -name airline-a -port 9001 -delay 200ms &
	go run ./cmd/mockprovider -name airline-b -port 9002 -delay 400ms -failrate 0.1 -format skyjson &
	go run ./cmd/mockprovider -name airline-c -port 9003 -delay 1200ms -format nested &

stop-mocks:
	-pkill -f mockprovider

run:
	go run ./cmd/api

build:
	go build -o bin/ ./cmd/...

test:
	go test ./...

test-race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -w .

vet:
	go vet ./...

# Benötigt golangci-lint: https://golangci-lint.run/welcome/install/
lint:
	golangci-lint run

docker-build:
	docker build -t flugsuche-go .

# API + Redis + drei Mock-Anbieter
up:
	docker compose up --build -d

down:
	docker compose down

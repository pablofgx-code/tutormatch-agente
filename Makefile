.PHONY: run build vet test cover docker-build docker-up docker-down

run:
	go run ./cmd/server/main.go

build:
	go build -o bin/agente ./cmd/server/main.go

vet:
	go vet ./...

test:
	go test -v ./internal/...

cover:
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out
	rm -f coverage.out

docker-build:
	docker build -t tutormatch-agente:latest .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down
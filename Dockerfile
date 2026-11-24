# Etapa 1: compilación
FROM golang:1.27-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /app/agente ./cmd/server/main.go

# Etapa 2: imagen final mínima
FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata \
    && adduser -D -H -u 10001 appuser

WORKDIR /app
COPY --from=builder /app/agente /app/agente

USER 10001:10001
EXPOSE 8081

ENTRYPOINT ["/app/agente"]
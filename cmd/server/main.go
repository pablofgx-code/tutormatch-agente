package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tutormatch/agente/internal/adapter"
	"github.com/tutormatch/agente/internal/domain"
	httpadapter "github.com/tutormatch/agente/internal/http"
	"github.com/tutormatch/agente/internal/mcp"
	"github.com/tutormatch/agente/internal/service"
)

func main() {
	port := getEnv("PORT", "8080")
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	geminiModel := getEnv("GEMINI_MODEL", "gemini-3.8-flash")
	bffURL := os.Getenv("BFF_URL")

	if geminiAPIKey == "" {
		log.Println("ADVERTENCIA: GEMINI_API_KEY no está definida; /agent/chat fallará hasta configurarla")
	}

	// Selección dinámica de adaptador: BFF real por GraphQL o Mock en memoria
	var bffClient domain.BFFClient
	if bffURL != "" {
		log.Printf("Conectando tools MCP al BFF real en %s", bffURL)
		bffClient = adapter.NewHTTPBFFClient(bffURL, nil)
	} else {
		log.Println("BFF_URL no definida: usando MockBFFClient en memoria para desarrollo")
		bffClient = adapter.NewMockBFFClient()
	}

	// Inyección de dependencias (Clean Architecture)
	registry := mcp.NewDefaultRegistry(bffClient)
	llmProvider := adapter.NewGeminiProvider(geminiAPIKey, geminiModel, nil)
	agentService := service.NewAgentService(llmProvider, registry, 6)
	handler := httpadapter.NewHandler(agentService, registry)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      180 * time.Second, // Da margen para múltiples turnos del agente
		IdleTimeout:       60 * time.Second,
	}

	// Apagado elegante (Graceful Shutdown)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Println("Apagando servidor MCP y Agente...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("error durante el apagado del servidor: %v", err)
		}
	}()

	log.Printf("Servidor MCP y Agente escuchando en el puerto %s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("error iniciando servidor HTTP: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

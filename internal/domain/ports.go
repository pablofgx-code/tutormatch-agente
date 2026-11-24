package domain

import (
	"context"
	"time"
)

// LLMProvider abstrae cualquier modelo de IA en la nube (Gemini, OpenAI, etc.).
type LLMProvider interface {
	GenerarTurno(ctx context.Context, mensajes []MensajeChat, tools []ToolDefinition) (ModelTurn, error)
}

// Interfaces segregadas por dominio (ISP) para que cada tool dependa solo de lo que usa.
type TutorClient interface {
	ListarRamos(ctx context.Context) ([]Ramo, error)
	BuscarTutores(ctx context.Context, ramoCodigo string) ([]TutorResumen, error)
}

type SesionesClient interface {
	BuscarDisponibilidad(ctx context.Context, ramoCodigo string, desde, hasta time.Time) ([]BloqueDisponibilidad, error)
	VerDisponibilidad(ctx context.Context, tutorID string, desde, hasta time.Time) ([]BloqueDisponibilidad, error)
	CrearReserva(ctx context.Context, tutorID, estudianteID, ramoCodigo string, inicio time.Time) (ReservaCreada, error)
}

// BFFClient compone las capacidades segregadas para el adaptador GraphQL hacia el BFF.
type BFFClient interface {
	TutorClient
	SesionesClient
}

// ConversationStore persiste el historial por usuario y conversacionId.
type ConversationStore interface {
	Obtener(usuarioID, conversacionID string) []MensajeChat
	Guardar(usuarioID, conversacionID string, mensajes []MensajeChat)
}

// Tool aplica el Patrón Strategy/Command para cada herramienta MCP.
type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, usuarioID string, args map[string]any) (any, error)
}

// ToolRegistry permite resolver herramientas en O(1) sin switch gigantes en el servicio.
type ToolRegistry interface {
	Get(name string) (Tool, bool)
	Definitions() []ToolDefinition
}

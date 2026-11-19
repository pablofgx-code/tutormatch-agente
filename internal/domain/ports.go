package domain

import "context"

// LLMProvider abstrae cualquier modelo de IA en la nube (Gemini, OpenAI, etc.).
type LLMProvider interface {
	GenerarTurno(ctx context.Context, mensajes []MensajeChat, tools []ToolDefinition) (ModelTurn, error)
}

// Interfaces segregadas por dominio (ISP) para que cada tool dependa solo de lo que usa.
type TutorClient interface {
	BuscarTutores(ctx context.Context, ramoCodigo string) ([]TutorResumen, error)
}

type SesionesClient interface {
	VerDisponibilidad(ctx context.Context, tutorID, dia string) ([]BloqueDisponibilidad, error)
	CrearReserva(ctx context.Context, tutorID, estudianteID, ramoCodigo, horario string) (ReservaCreada, error)
}

type MaterialClient interface {
	GenerarQuiz(ctx context.Context, sesionID, ramoCodigo string) (QuizResultado, error)
}

// BFFClient compone las capacidades segregadas para el adaptador HTTP/GraphQL hacia el BFF.
type BFFClient interface {
	TutorClient
	SesionesClient
	MaterialClient
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

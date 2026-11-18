package domain

import "context"

// LLMProvider abstrae el modelo de IA en la nube (Gemini) y su loop de tool calling[cite: 6].
type LLMProvider interface {
	GenerarConTools(ctx context.Context, historial []any, promptUsuario string, resultadosPrevios []ToolResult) (ModelTurn, []any, error)
}

// BFFClient abstrae las operaciones que ejecutan las 4 tools MCP contra el sistema[cite: 6].
type BFFClient interface {
	BuscarTutores(ctx context.Context, ramoCodigo string) ([]TutorResumen, error)
	VerDisponibilidad(ctx context.Context, tutorID, dia string) ([]BloqueDisponibilidad, error)
	CrearReserva(ctx context.Context, tutorID, estudianteID, ramoCodigo, horario string) (ReservaCreada, error)
	GenerarQuiz(ctx context.Context, sesionID, ramoCodigo string) (QuizResultado, error)
}

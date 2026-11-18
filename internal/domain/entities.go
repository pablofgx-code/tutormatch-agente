package domain

import "time"

// ChatRequest representa el mensaje que envía el BFF al endpoint POST /agent/chat[cite: 6].
type ChatRequest struct {
	UsuarioID string `json:"usuario_id"`
	Mensaje   string `json:"mensaje"`
}

// ChatResponse es la respuesta final redactada por el agente hacia el BFF[cite: 6].
type ChatResponse struct {
	Respuesta string `json:"respuesta"`
}

// TutorResumen representa un tutor activo devuelto por la tool buscar_tutores[cite: 6].
type TutorResumen struct {
	ID     string   `json:"id"`
	Nombre string   `json:"nombre"`
	Email  string   `json:"email"`
	Ramos  []string `json:"ramos"`
}

// BloqueDisponibilidad representa un horario libre de un tutor en Sesiones[cite: 5, 6].
type BloqueDisponibilidad struct {
	ID      string `json:"id"`
	TutorID string `json:"tutor_id"`
	Dia     string `json:"dia"`
	HoraIni string `json:"hora_ini"`
	HoraFin string `json:"hora_fin"`
}

// ReservaCreada representa la sesión confirmada/pendiente creada en Sesiones[cite: 5, 6].
type ReservaCreada struct {
	SesionID     string    `json:"sesion_id"`
	TutorID      string    `json:"tutor_id"`
	EstudianteID string    `json:"estudiante_id"`
	RamoCodigo   string    `json:"ramo_codigo"`
	Estado       string    `json:"estado"`
	CreadaEn     time.Time `json:"creada_en"`
}

// PreguntaQuiz y QuizResultado representan el quiz generado a partir del material[cite: 5, 6].
type PreguntaQuiz struct {
	Pregunta  string   `json:"pregunta"`
	Opciones  []string `json:"opciones"`
	Respuesta string   `json:"respuesta"`
}

type QuizResultado struct {
	RamoCodigo string         `json:"ramo_codigo"`
	Preguntas  []PreguntaQuiz `json:"preguntas"`
}

// ToolCall y ToolResult modelan el intercambio de herramientas dentro del loop del agente[cite: 6].
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content any    `json:"content"`
}

// ModelTurn representa la respuesta del modelo de IA: o pide ejecutar tools o entrega texto final[cite: 6].
type ModelTurn struct {
	Text      string     `json:"text,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

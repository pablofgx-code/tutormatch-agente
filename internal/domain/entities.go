package domain

import (
	"strings"
	"time"
)

// ChatRequest representa el mensaje que envía el BFF al endpoint POST /agent/chat.
type ChatRequest struct {
	UsuarioID string        `json:"usuario_id"`
	Mensaje   string        `json:"mensaje"`
	Historial []MensajeChat `json:"historial,omitempty"`
}

func (r ChatRequest) Validar() error {
	if strings.TrimSpace(r.UsuarioID) == "" || strings.TrimSpace(r.Mensaje) == "" {
		return ErrArgumentoInvalido
	}
	return nil
}

// ChatResponse es la respuesta final redactada por el agente hacia el BFF.
type ChatResponse struct {
	Respuesta string `json:"respuesta"`
}

type RolMensaje string

const (
	RolUsuario     RolMensaje = "user"
	RolModelo      RolMensaje = "model"
	RolHerramienta RolMensaje = "tool"
)

// MensajeChat modela un turno agnóstico al proveedor de IA.
type MensajeChat struct {
	Rol        RolMensaje  `json:"rol"`
	Texto      string      `json:"texto,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

// ToolDefinition describe el contrato JSON Schema que consume tanto MCP como el LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type TutorResumen struct {
	ID     string   `json:"id"`
	Nombre string   `json:"nombre"`
	Email  string   `json:"email"`
	Ramos  []string `json:"ramos"`
}

type BloqueDisponibilidad struct {
	ID      string `json:"id"`
	TutorID string `json:"tutor_id"`
	Dia     string `json:"dia"`
	HoraIni string `json:"hora_ini"`
	HoraFin string `json:"hora_fin"`
}

type ReservaCreada struct {
	SesionID     string    `json:"sesion_id"`
	TutorID      string    `json:"tutor_id"`
	EstudianteID string    `json:"estudiante_id"`
	RamoCodigo   string    `json:"ramo_codigo"`
	Estado       string    `json:"estado"`
	CreadaEn     time.Time `json:"creada_en"`
}

type PreguntaQuiz struct {
	Pregunta          string   `json:"pregunta"`
	Opciones          []string `json:"opciones"`
	RespuestaCorrecta string   `json:"respuesta_correcta"`
}

type QuizResultado struct {
	RamoCodigo string         `json:"ramo_codigo"`
	Preguntas  []PreguntaQuiz `json:"preguntas"`
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Signature string         `json:"signature,omitempty"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content any    `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

type ModelTurn struct {
	Text      string     `json:"text,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

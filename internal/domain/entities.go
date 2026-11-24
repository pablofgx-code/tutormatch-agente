package domain

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxMensajeRunes      = 2000
	maxConversacionIDLen = 128
)

type authTokenKey struct{}

// ConAuthToken guarda la cabecera Authorization (Bearer <token>) en el contexto para reenviarla al BFF.
func ConAuthToken(ctx context.Context, authHeader string) context.Context {
	return context.WithValue(ctx, authTokenKey{}, strings.TrimSpace(authHeader))
}

// AuthTokenDesdeContexto recupera la cabecera Authorization del contexto si existe.
func AuthTokenDesdeContexto(ctx context.Context) string {
	val, _ := ctx.Value(authTokenKey{}).(string)
	return val
}

// ChatRequest representa el cuerpo que envía el BFF a POST /agent/chat (contrato rest/agente.md).
type ChatRequest struct {
	UsuarioID      string `json:"usuarioId"`
	ConversacionID string `json:"conversacionId,omitempty"`
	Mensaje        string `json:"mensaje"`
}

func (r ChatRequest) Validar() error {
	usuarioID := strings.TrimSpace(r.UsuarioID)
	mensaje := strings.TrimSpace(r.Mensaje)
	convID := strings.TrimSpace(r.ConversacionID)

	if usuarioID == "" || mensaje == "" {
		return ErrArgumentoInvalido
	}
	if utf8.RuneCountInString(mensaje) > maxMensajeRunes {
		return ErrArgumentoInvalido
	}
	if len(convID) > maxConversacionIDLen {
		return ErrArgumentoInvalido
	}
	return nil
}

// AccionAgente informa al BFF y al frontend qué mutaciones ejecutó el agente para refrescar vistas.
type AccionAgente struct {
	Tipo string `json:"tipo"`
	ID   string `json:"id"`
}

// ChatResponse es la respuesta final devuelta al BFF.
type ChatResponse struct {
	ConversacionID string         `json:"conversacionId"`
	Respuesta      string         `json:"respuesta"`
	Acciones       []AccionAgente `json:"acciones"`
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

type Ramo struct {
	Codigo string `json:"codigo"`
	Nombre string `json:"nombre"`
}

type RamoTutor struct {
	Ramo   Ramo `json:"ramo"`
	Activo bool `json:"activo"`
}

type TutorResumen struct {
	ID                   string      `json:"id"`
	Nombre               string      `json:"nombre"`
	Email                string      `json:"email"`
	PromedioEvaluaciones float64     `json:"promedioEvaluaciones"`
	CantidadEvaluaciones int         `json:"cantidadEvaluaciones"`
	Ramos                []RamoTutor `json:"ramos"`
}

type BloqueDisponibilidad struct {
	ID         string    `json:"id"`
	TutorID    string    `json:"tutorId"`
	RamoCodigo string    `json:"ramoCodigo"`
	Inicio     time.Time `json:"inicio"`
	Fin        time.Time `json:"fin"`
	Libre      bool      `json:"libre"`
}

type ReservaCreada struct {
	ID           string    `json:"id"`
	TutorID      string    `json:"tutorId"`
	EstudianteID string    `json:"estudianteId"`
	RamoCodigo   string    `json:"ramoCodigo"`
	Inicio       time.Time `json:"inicio"`
	Fin          time.Time `json:"fin"`
	Estado       string    `json:"estado"`
	CreadaEn     time.Time `json:"creadaEn"`
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

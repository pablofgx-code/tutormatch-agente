package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/tutormatch/agente/internal/domain"
)

// Registry implementa domain.ToolRegistry con búsqueda en O(1) y orden determinista.
type Registry struct {
	tools map[string]domain.Tool
	orden []string
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]domain.Tool),
	}
}

func (r *Registry) Register(t domain.Tool) {
	name := t.Definition().Name
	if _, existe := r.tools[name]; !existe {
		r.orden = append(r.orden, name)
	}
	r.tools[name] = t
}

func (r *Registry) Get(name string) (domain.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Definitions() []domain.ToolDefinition {
	defs := make([]domain.ToolDefinition, 0, len(r.orden))
	for _, name := range r.orden {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}

// NewDefaultRegistry (Factory) registra las 4 tools oficiales de TutorMatch.
func NewDefaultRegistry(bff domain.BFFClient) *Registry {
	reg := NewRegistry()
	reg.Register(&BuscarTutoresTool{client: bff})
	reg.Register(&VerDisponibilidadTool{client: bff})
	reg.Register(&CrearReservaTool{client: bff})
	reg.Register(&GenerarQuizTool{client: bff})
	return reg
}

// Helper DRY para extraer y validar argumentos de texto enviados por el modelo de IA.
func extraerString(args map[string]any, key string, requerido bool) (string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		if requerido {
			return "", fmt.Errorf("%w: falta el parámetro '%s'", domain.ErrArgumentoInvalido, key)
		}
		return "", nil
	}

	val, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: el parámetro '%s' debe ser texto", domain.ErrArgumentoInvalido, key)
	}

	val = strings.TrimSpace(val)
	if requerido && val == "" {
		return "", fmt.Errorf("%w: el parámetro '%s' no puede estar vacío", domain.ErrArgumentoInvalido, key)
	}
	return val, nil
}

// --- Tool 1: buscar_tutores ---

type BuscarTutoresTool struct {
	client domain.TutorClient
}

func (t *BuscarTutoresTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "buscar_tutores",
		Description: "Busca tutores activos que hayan aprobado un ramo específico usando su código oficial (ej. MAT1610, MAT1620, IIC1103, IIC2133, FIS1514).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ramo_codigo": map[string]any{
					"type":        "string",
					"description": "Código del ramo en mayúsculas, por ejemplo MAT1620.",
				},
			},
			"required": []string{"ramo_codigo"},
		},
	}
}

func (t *BuscarTutoresTool) Execute(ctx context.Context, _ string, args map[string]any) (any, error) {
	ramoCodigo, err := extraerString(args, "ramo_codigo", true)
	if err != nil {
		return nil, err
	}
	return t.client.BuscarTutores(ctx, strings.ToUpper(ramoCodigo))
}

// --- Tool 2: ver_disponibilidad ---

type VerDisponibilidadTool struct {
	client domain.SesionesClient
}

func (t *VerDisponibilidadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "ver_disponibilidad",
		Description: "Consulta los bloques horarios libres de un tutor específico. Opcionalmente permite filtrar por día de la semana.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tutor_id": map[string]any{
					"type":        "string",
					"description": "UUID del tutor obtenido previamente con buscar_tutores.",
				},
				"dia": map[string]any{
					"type":        "string",
					"description": "Día de la semana opcional (ej. lunes, jueves).",
				},
			},
			"required": []string{"tutor_id"},
		},
	}
}

func (t *VerDisponibilidadTool) Execute(ctx context.Context, _ string, args map[string]any) (any, error) {
	tutorID, err := extraerString(args, "tutor_id", true)
	if err != nil {
		return nil, err
	}
	dia, err := extraerString(args, "dia", false)
	if err != nil {
		return nil, err
	}
	return t.client.VerDisponibilidad(ctx, tutorID, strings.ToLower(dia))
}

// --- Tool 3: crear_reserva ---

type CrearReservaTool struct {
	client domain.SesionesClient
}

func (t *CrearReservaTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "crear_reserva",
		Description: "Crea una reserva de tutoría en estado PENDIENTE solo cuando el estudiante ya confirmó el tutor y el horario.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tutor_id": map[string]any{
					"type":        "string",
					"description": "UUID del tutor seleccionado.",
				},
				"ramo_codigo": map[string]any{
					"type":        "string",
					"description": "Código oficial del ramo (ej. MAT1620).",
				},
				"horario": map[string]any{
					"type":        "string",
					"description": "Fecha y hora de inicio en formato ISO-8601 (ej. 2026-11-26T15:00:00Z).",
				},
			},
			"required": []string{"tutor_id", "ramo_codigo", "horario"},
		},
	}
}

func (t *CrearReservaTool) Execute(ctx context.Context, usuarioID string, args map[string]any) (any, error) {
	tutorID, err := extraerString(args, "tutor_id", true)
	if err != nil {
		return nil, err
	}
	ramoCodigo, err := extraerString(args, "ramo_codigo", true)
	if err != nil {
		return nil, err
	}
	horario, err := extraerString(args, "horario", true)
	if err != nil {
		return nil, err
	}
	// usuarioID proviene del JWT validado en el BFF, nunca del modelo de IA.
	return t.client.CrearReserva(ctx, tutorID, usuarioID, strings.ToUpper(ramoCodigo), horario)
}

// --- Tool 4: generar_quiz ---

type GenerarQuizTool struct {
	client domain.MaterialClient
}

func (t *GenerarQuizTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "generar_quiz",
		Description: "Genera un quiz de práctica basado en los apuntes y material compartido de un ramo o sesión.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ramo_codigo": map[string]any{
					"type":        "string",
					"description": "Código del ramo sobre el cual generar preguntas (ej. MAT1620).",
				},
				"sesion_id": map[string]any{
					"type":        "string",
					"description": "UUID opcional de una sesión finalizada para usar sus apuntes específicos.",
				},
			},
			"required": []string{"ramo_codigo"},
		},
	}
}

func (t *GenerarQuizTool) Execute(ctx context.Context, _ string, args map[string]any) (any, error) {
	ramoCodigo, err := extraerString(args, "ramo_codigo", true)
	if err != nil {
		return nil, err
	}
	sesionID, err := extraerString(args, "sesion_id", false)
	if err != nil {
		return nil, err
	}
	return t.client.GenerarQuiz(ctx, sesionID, strings.ToUpper(ramoCodigo))
}

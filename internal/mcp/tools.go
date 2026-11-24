package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

var (
	diasSemana = map[time.Weekday]string{
		time.Sunday:    "domingo",
		time.Monday:    "lunes",
		time.Tuesday:   "martes",
		time.Wednesday: "miércoles",
		time.Thursday:  "jueves",
		time.Friday:    "viernes",
		time.Saturday:  "sábado",
	}
	nombresBloque = map[string]string{
		"08:10": "Bloque A",
		"09:55": "Bloque B",
		"11:40": "Bloque C",
		"14:30": "Bloque D",
		"16:15": "Bloque E",
		"18:00": "Bloque F",
	}
)

type bloqueEnriquecido struct {
	ID           string    `json:"id"`
	TutorID      string    `json:"tutorId"`
	RamoCodigo   string    `json:"ramoCodigo"`
	Inicio       time.Time `json:"inicio"`
	Fin          time.Time `json:"fin"`
	Libre        bool      `json:"libre"`
	HorarioChile string    `json:"horarioChile"`
}

func enriquecerBloques(bloques []domain.BloqueDisponibilidad) []bloqueEnriquecido {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		loc = time.FixedZone("America/Santiago", -3*3600)
	}

	res := make([]bloqueEnriquecido, 0, len(bloques))
	for _, b := range bloques {
		iniLocal := b.Inicio.In(loc)
		finLocal := b.Fin.In(loc)
		etiqueta := fmt.Sprintf("%s %s %s - %s",
			diasSemana[iniLocal.Weekday()],
			iniLocal.Format("02/01/2006"),
			iniLocal.Format("15:04"),
			finLocal.Format("15:04"),
		)
		if nombre, ok := nombresBloque[iniLocal.Format("15:04")]; ok {
			etiqueta += " (" + nombre + ")"
		}
		res = append(res, bloqueEnriquecido{
			ID:           b.ID,
			TutorID:      b.TutorID,
			RamoCodigo:   b.RamoCodigo,
			Inicio:       b.Inicio,
			Fin:          b.Fin,
			Libre:        b.Libre,
			HorarioChile: etiqueta,
		})
	}
	return res
}

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

// NewDefaultRegistry (Factory) registra las tools MCP alineadas con el esquema real del BFF.
func NewDefaultRegistry(bff domain.BFFClient) *Registry {
	reg := NewRegistry()
	reg.Register(&ListarRamosTool{client: bff})
	reg.Register(&BuscarTutoresTool{client: bff})
	reg.Register(&BuscarDisponibilidadTool{client: bff})
	reg.Register(&VerDisponibilidadTool{client: bff})
	reg.Register(&CrearReservaTool{client: bff})
	return reg
}

// Helpers DRY para extraer y validar argumentos enviados por el modelo de IA.
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

func extraerFechaUTC(args map[string]any, key string) (time.Time, error) {
	raw, err := extraerString(args, key, true)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: '%s' debe estar en formato ISO-8601 UTC (ej. 2026-11-26T17:30:00Z)", domain.ErrArgumentoInvalido, key)
	}
	return parsed.UTC(), nil
}

// --- Tool 1: listar_ramos ---

type ListarRamosTool struct {
	client domain.TutorClient
}

func (t *ListarRamosTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "listar_ramos",
		Description: "Devuelve el catálogo oficial de ramos con sus códigos y nombres (ej. MAT1610, MAT1620).",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *ListarRamosTool) Execute(ctx context.Context, _ string, _ map[string]any) (any, error) {
	return t.client.ListarRamos(ctx)
}

// --- Tool 2: buscar_tutores ---

type BuscarTutoresTool struct {
	client domain.TutorClient
}

func (t *BuscarTutoresTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "buscar_tutores",
		Description: "Busca tutores activos que imparten un ramo específico usando su código oficial, incluyendo nombre y promedio de evaluaciones.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ramo_codigo": map[string]any{
					"type":        "string",
					"description": "Código oficial del ramo en mayúsculas, por ejemplo MAT1620.",
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

// --- Tool 3: buscar_disponibilidad ---

type BuscarDisponibilidadTool struct {
	client domain.SesionesClient
}

func (t *BuscarDisponibilidadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "buscar_disponibilidad",
		Description: "Busca todas las casillas libres de 90 minutos de cualquier tutor para un ramo dentro de un rango de fechas UTC [desde, hasta).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ramo_codigo": map[string]any{
					"type":        "string",
					"description": "Código oficial del ramo en mayúsculas (ej. MAT1620).",
				},
				"desde": map[string]any{
					"type":        "string",
					"description": "Inicio del rango de búsqueda en ISO-8601 UTC (ej. 2026-11-26T17:00:00Z).",
				},
				"hasta": map[string]any{
					"type":        "string",
					"description": "Fin exclusivo del rango de búsqueda en ISO-8601 UTC (ej. 2026-11-27T00:00:00Z).",
				},
			},
			"required": []string{"ramo_codigo", "desde", "hasta"},
		},
	}
}

func (t *BuscarDisponibilidadTool) Execute(ctx context.Context, _ string, args map[string]any) (any, error) {
	ramoCodigo, err := extraerString(args, "ramo_codigo", true)
	if err != nil {
		return nil, err
	}
	desde, err := extraerFechaUTC(args, "desde")
	if err != nil {
		return nil, err
	}
	hasta, err := extraerFechaUTC(args, "hasta")
	if err != nil {
		return nil, err
	}
	if !hasta.After(desde) {
		return nil, fmt.Errorf("%w: 'hasta' debe ser posterior a 'desde'", domain.ErrArgumentoInvalido)
	}
	bloques, err := t.client.BuscarDisponibilidad(ctx, strings.ToUpper(ramoCodigo), desde, hasta)
	if err != nil {
		return nil, err
	}
	return enriquecerBloques(bloques), nil
}

// --- Tool 4: ver_disponibilidad ---

type VerDisponibilidadTool struct {
	client domain.SesionesClient
}

func (t *VerDisponibilidadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "ver_disponibilidad",
		Description: "Consulta las casillas de 90 minutos (libres u ocupadas) de un tutor específico en un rango de fechas UTC [desde, hasta).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tutor_id": map[string]any{
					"type":        "string",
					"description": "UUID del tutor obtenido previamente con buscar_tutores.",
				},
				"desde": map[string]any{
					"type":        "string",
					"description": "Inicio del rango en ISO-8601 UTC.",
				},
				"hasta": map[string]any{
					"type":        "string",
					"description": "Fin exclusivo del rango en ISO-8601 UTC.",
				},
			},
			"required": []string{"tutor_id", "desde", "hasta"},
		},
	}
}

func (t *VerDisponibilidadTool) Execute(ctx context.Context, _ string, args map[string]any) (any, error) {
	tutorID, err := extraerString(args, "tutor_id", true)
	if err != nil {
		return nil, err
	}
	desde, err := extraerFechaUTC(args, "desde")
	if err != nil {
		return nil, err
	}
	hasta, err := extraerFechaUTC(args, "hasta")
	if err != nil {
		return nil, err
	}
	if !hasta.After(desde) {
		return nil, fmt.Errorf("%w: 'hasta' debe ser posterior a 'desde'", domain.ErrArgumentoInvalido)
	}
	bloques, err := t.client.VerDisponibilidad(ctx, tutorID, desde, hasta)
	if err != nil {
		return nil, err
	}
	return enriquecerBloques(bloques), nil
}

// --- Tool 5: crear_reserva ---

type CrearReservaTool struct {
	client domain.SesionesClient
}

func (t *CrearReservaTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "crear_reserva",
		Description: "Reserva una casilla libre de 90 minutos en estado PENDIENTE. Ejecutar ÚNICAMENTE cuando el estudiante ya confirmó el tutor y el horario exacto.",
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
				"inicio": map[string]any{
					"type":        "string",
					"description": "Timestamp exacto de inicio de la casilla libre en ISO-8601 UTC (ej. 2026-11-26T17:30:00Z).",
				},
			},
			"required": []string{"tutor_id", "ramo_codigo", "inicio"},
		},
	}
}

func (t *CrearReservaTool) Execute(ctx context.Context, usuarioID string, args map[string]any) (any, error) {
	usuarioID = strings.TrimSpace(usuarioID)
	if usuarioID == "" && domain.AuthTokenDesdeContexto(ctx) == "" {
		return nil, fmt.Errorf("%w: falta la autenticación del estudiante", domain.ErrArgumentoInvalido)
	}

	tutorID, err := extraerString(args, "tutor_id", true)
	if err != nil {
		return nil, err
	}
	ramoCodigo, err := extraerString(args, "ramo_codigo", true)
	if err != nil {
		return nil, err
	}
	inicio, err := extraerFechaUTC(args, "inicio")
	if err != nil {
		return nil, err
	}
	return t.client.CrearReserva(ctx, tutorID, usuarioID, strings.ToUpper(ramoCodigo), inicio)
}

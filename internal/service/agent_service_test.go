package service

import (
	"context"
	"errors"
	"testing"

	"github.com/tutormatch/agente/internal/adapter"
	"github.com/tutormatch/agente/internal/domain"
	"github.com/tutormatch/agente/internal/mcp"
)

type mockLLM struct {
	turnos   []domain.ModelTurn
	err      error
	llamadas int
	ultimos  []domain.MensajeChat
}

func (m *mockLLM) GenerarTurno(_ context.Context, mensajes []domain.MensajeChat, _ []domain.ToolDefinition) (domain.ModelTurn, error) {
	m.ultimos = mensajes
	if m.err != nil {
		return domain.ModelTurn{}, m.err
	}
	idx := m.llamadas
	m.llamadas++
	if idx >= len(m.turnos) {
		return m.turnos[len(m.turnos)-1], nil
	}
	return m.turnos[idx], nil
}

func TestProcesarChat_ValidacionYErrores(t *testing.T) {
	reg := mcp.NewDefaultRegistry(adapter.NewMockBFFClient())
	svc := NewAgentService(&mockLLM{}, reg, 0) // 0 prueba que se aplique defaultMaxIteraciones
	ctx := context.Background()

	// 1. Entrada inválida (sin usuario o sin mensaje)
	_, err := svc.ProcesarChat(ctx, domain.ChatRequest{UsuarioID: "", Mensaje: "Hola"})
	if !errors.Is(err, domain.ErrArgumentoInvalido) {
		t.Fatalf("se esperaba ErrArgumentoInvalido, obtuve %v", err)
	}

	// 2. Contexto cancelado antes de llamar al LLM
	ctxCancel, cancel := context.WithCancel(ctx)
	cancel()
	_, err = svc.ProcesarChat(ctxCancel, domain.ChatRequest{UsuarioID: "u-1", Mensaje: "Hola"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("se esperaba context.Canceled, obtuve %v", err)
	}

	// 3. Error del proveedor de IA envuelto en ErrProveedorIA
	svcErr := NewAgentService(&mockLLM{err: errors.New("api caída")}, reg, 3)
	_, err = svcErr.ProcesarChat(ctx, domain.ChatRequest{UsuarioID: "u-1", Mensaje: "Hola"})
	if !errors.Is(err, domain.ErrProveedorIA) {
		t.Fatalf("se esperaba ErrProveedorIA, obtuve %v", err)
	}
}

func TestProcesarChat_FlujoConToolsYParalelismo(t *testing.T) {
	reg := mcp.NewDefaultRegistry(adapter.NewMockBFFClient())
	llm := &mockLLM{
		turnos: []domain.ModelTurn{
			// Turno 1 (Fast-path: 1 sola tool)
			{
				ToolCalls: []domain.ToolCall{
					{ID: "c1", Name: "buscar_tutores", Arguments: map[string]any{"ramo_codigo": "MAT1620"}},
				},
			},
			// Turno 2 (Fan-Out / Fan-In: 2 tools en paralelo)
			{
				ToolCalls: []domain.ToolCall{
					{ID: "c2", Name: "ver_disponibilidad", Arguments: map[string]any{"tutor_id": "11111111-1111-1111-1111-111111111111", "dia": "jueves"}},
					{ID: "c3", Name: "ver_disponibilidad", Arguments: map[string]any{"tutor_id": "22222222-2222-2222-2222-222222222222", "dia": "jueves"}},
				},
			},
			// Turno 3 (Respuesta final)
			{
				Text: "Tienes disponibles a Camila Rojas (15:00-16:30) y Matías Vega (17:00-18:30) el jueves.",
			},
		},
	}

	svc := NewAgentService(llm, reg, 6)
	resp, err := svc.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID: "est-123",
		Mensaje:   "Necesito ayuda con Cálculo II el jueves",
	})
	if err != nil {
		t.Fatalf("no se esperaba error, obtuve %v", err)
	}
	if resp.Respuesta == "" || llm.llamadas != 3 {
		t.Fatalf("se esperaban 3 turnos y respuesta no vacía, llamadas=%d resp=%q", llm.llamadas, resp.Respuesta)
	}
}

func TestProcesarChat_ReservaUsaUsuarioAutenticado(t *testing.T) {
	reg := mcp.NewDefaultRegistry(adapter.NewMockBFFClient())
	llm := &mockLLM{
		turnos: []domain.ModelTurn{
			{
				ToolCalls: []domain.ToolCall{
					{
						ID:   "c1",
						Name: "crear_reserva",
						Arguments: map[string]any{
							"tutor_id":    "11111111-1111-1111-1111-111111111111",
							"ramo_codigo": "MAT1620",
							"horario":     "2026-11-26T15:00:00Z",
						},
					},
				},
			},
			{Text: "Reserva creada con éxito."},
		},
	}

	svc := NewAgentService(llm, reg, 4)
	_, err := svc.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID: "est-autenticado-999",
		Mensaje:   "Confirmo con Camila el jueves a las 15:00",
	})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	// Verifica que el resultado de la tool llevó el ID real del estudiante
	var reserva domain.ReservaCreada
	encontrada := false
	for _, msg := range llm.ultimos {
		if msg.Rol == domain.RolHerramienta && msg.ToolResult != nil {
			if r, ok := msg.ToolResult.Content.(domain.ReservaCreada); ok {
				reserva = r
				encontrada = true
			}
		}
	}
	if !encontrada || reserva.EstudianteID != "est-autenticado-999" {
		t.Fatalf("se esperaba EstudianteID='est-autenticado-999', obtuve %+v", reserva)
	}
}

func TestProcesarChat_ErroresRecuperablesYMaxIteraciones(t *testing.T) {
	reg := mcp.NewDefaultRegistry(adapter.NewMockBFFClient())

	// 1. Fail-soft: tool desconocida + parámetro faltante no botan la petición
	llmFailSoft := &mockLLM{
		turnos: []domain.ModelTurn{
			{
				ToolCalls: []domain.ToolCall{
					{ID: "c1", Name: "tool_inexistente", Arguments: map[string]any{}},
					{ID: "c2", Name: "buscar_tutores", Arguments: map[string]any{}}, // falta ramo_codigo
				},
			},
			{Text: "¿Para qué código de ramo necesitas un tutor?"},
		},
	}
	svc := NewAgentService(llmFailSoft, reg, 4)
	resp, err := svc.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID: "est-1",
		Mensaje:   "Busca un tutor",
	})
	if err != nil || resp.Respuesta == "" {
		t.Fatalf("se esperaba recuperación exitosa, obtuve err=%v resp=%q", err, resp.Respuesta)
	}

	// 2. Bucle infinito detenido por maxIteraciones
	llmBucle := &mockLLM{
		turnos: []domain.ModelTurn{
			{
				ToolCalls: []domain.ToolCall{
					{ID: "loop", Name: "buscar_tutores", Arguments: map[string]any{"ramo_codigo": "MAT1620"}},
				},
			},
		},
	}
	svcCorto := NewAgentService(llmBucle, reg, 2)
	_, err = svcCorto.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID: "est-1",
		Mensaje:   "Busca infinitamente",
	})
	if !errors.Is(err, domain.ErrMaxIteraciones) {
		t.Fatalf("se esperaba ErrMaxIteraciones, obtuve %v", err)
	}
}

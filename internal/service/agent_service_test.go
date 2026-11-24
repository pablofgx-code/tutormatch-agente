package service

import (
	"context"
	"errors"
	"testing"
	"time"

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
	svc := NewAgentService(&mockLLM{}, reg, nil, 0)
	ctx := context.Background()

	if _, err := svc.ProcesarChat(ctx, domain.ChatRequest{UsuarioID: "", Mensaje: "Hola"}); !errors.Is(err, domain.ErrArgumentoInvalido) {
		t.Fatalf("esperaba ErrArgumentoInvalido, obtuve %v", err)
	}

	ctxCancel, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.ProcesarChat(ctxCancel, domain.ChatRequest{UsuarioID: "u-1", Mensaje: "Hola"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("esperaba context.Canceled, obtuve %v", err)
	}

	svcErr := NewAgentService(&mockLLM{err: errors.New("api caída")}, reg, nil, 3)
	if _, err := svcErr.ProcesarChat(ctx, domain.ChatRequest{UsuarioID: "u-1", Mensaje: "Hola"}); !errors.Is(err, domain.ErrProveedorIA) {
		t.Fatalf("esperaba ErrProveedorIA, obtuve %v", err)
	}
}

func TestProcesarChat_FlujoMultiTurnoParalelismoYAcciones(t *testing.T) {
	bff := adapter.NewMockBFFClient()
	reg := mcp.NewDefaultRegistry(bff)
	store := adapter.NewLRUConversationStore(10, time.Minute, 30)

	desde := time.Now().UTC().Add(-time.Hour)
	hasta := time.Now().UTC().AddDate(0, 0, 14)
	bloques, err := bff.BuscarDisponibilidad(context.Background(), "MAT1620", desde, hasta)
	if err != nil || len(bloques) == 0 {
		t.Fatalf("no se encontraron bloques mock: %v", err)
	}
	primero := bloques[0]

	llm := &mockLLM{
		turnos: []domain.ModelTurn{
			// Turno 1, Paso 1: Fan-Out paralelo de 2 tools
			{
				ToolCalls: []domain.ToolCall{
					{ID: "c1", Name: "buscar_tutores", Arguments: map[string]any{"ramo_codigo": "MAT1620"}},
					{ID: "c2", Name: "buscar_disponibilidad", Arguments: map[string]any{
						"ramo_codigo": "MAT1620",
						"desde":       desde.Format(time.RFC3339),
						"hasta":       hasta.Format(time.RFC3339),
					}},
				},
			},
			// Turno 1, Paso 2: Respuesta proponiendo opciones
			{Text: "Camila Rojas está disponible. ¿Confirmas?"},
			// Turno 2, Paso 1: Fast-path de 1 tool (crear_reserva)
			{
				ToolCalls: []domain.ToolCall{
					{
						ID:   "c3",
						Name: "crear_reserva",
						Arguments: map[string]any{
							"tutor_id":    primero.TutorID,
							"ramo_codigo": "MAT1620",
							"inicio":      primero.Inicio.Format(time.RFC3339),
						},
					},
				},
			},
			// Turno 2, Paso 2: Confirmación final
			{Text: "Reserva creada con éxito."},
		},
	}

	svc := NewAgentService(llm, reg, store, 6)

	// Turno 1: crea conversacionId y no devuelve acciones
	resp1, err := svc.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID: "est-autenticado-999",
		Mensaje:   "Necesito ayuda con Cálculo II",
	})
	if err != nil || resp1.ConversacionID == "" || len(resp1.Acciones) != 0 {
		t.Fatalf("respuesta turno 1 inesperada: %+v err=%v", resp1, err)
	}

	// Turno 2: reutiliza conversacionId y devuelve accion sesion.creada
	resp2, err := svc.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID:      "est-autenticado-999",
		ConversacionID: resp1.ConversacionID,
		Mensaje:        "Sí, confirmo",
	})
	if err != nil {
		t.Fatalf("error turno 2: %v", err)
	}
	if resp2.ConversacionID != resp1.ConversacionID || len(resp2.Acciones) != 1 || resp2.Acciones[0].Tipo != "sesion.creada" {
		t.Fatalf("acciones inesperadas en turno 2: %+v", resp2)
	}
	// Verifica que el turno 2 recibió el historial persistido del turno 1
	if len(llm.ultimos) < 5 {
		t.Fatalf("no se preservó el historial entre turnos, len=%d", len(llm.ultimos))
	}
}

func TestProcesarChat_ErroresRecuperablesYMaxIteraciones(t *testing.T) {
	reg := mcp.NewDefaultRegistry(adapter.NewMockBFFClient())

	llmFailSoft := &mockLLM{
		turnos: []domain.ModelTurn{
			{
				ToolCalls: []domain.ToolCall{
					{ID: "c1", Name: "tool_inexistente", Arguments: map[string]any{}},
					{ID: "c2", Name: "buscar_tutores", Arguments: map[string]any{}},
				},
			},
			{Text: "¿Para qué código de ramo necesitas un tutor?"},
		},
	}
	svc := NewAgentService(llmFailSoft, reg, nil, 4)
	resp, err := svc.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID: "est-1",
		Mensaje:   "Busca un tutor",
	})
	if err != nil || resp.Respuesta == "" {
		t.Fatalf("se esperaba recuperación fail-soft, err=%v resp=%+v", err, resp)
	}

	llmBucle := &mockLLM{
		turnos: []domain.ModelTurn{
			{ToolCalls: []domain.ToolCall{{ID: "loop", Name: "buscar_tutores", Arguments: map[string]any{"ramo_codigo": "MAT1620"}}}},
		},
	}
	svcCorto := NewAgentService(llmBucle, reg, nil, 2)
	if _, err = svcCorto.ProcesarChat(context.Background(), domain.ChatRequest{
		UsuarioID: "est-1",
		Mensaje:   "Busca infinitamente",
	}); !errors.Is(err, domain.ErrMaxIteraciones) {
		t.Fatalf("se esperaba ErrMaxIteraciones, obtuve %v", err)
	}
}

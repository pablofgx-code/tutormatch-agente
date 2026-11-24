package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

const defaultMaxIteraciones = 6

// AgentService orquesta exclusivamente el loop de conversación entre el LLM y las tools MCP (SRP).
type AgentService struct {
	llm            domain.LLMProvider
	registry       domain.ToolRegistry
	store          domain.ConversationStore
	maxIteraciones int
}

func NewAgentService(llm domain.LLMProvider, registry domain.ToolRegistry, store domain.ConversationStore, maxIteraciones int) *AgentService {
	if maxIteraciones <= 0 {
		maxIteraciones = defaultMaxIteraciones
	}
	return &AgentService{
		llm:            llm,
		registry:       registry,
		store:          store,
		maxIteraciones: maxIteraciones,
	}
}

// ProcesarChat ejecuta el loop del agente, persiste el historial en el store y reporta acciones.
func (s *AgentService) ProcesarChat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error) {
	if err := req.Validar(); err != nil {
		return domain.ChatResponse{}, err
	}

	convID := strings.TrimSpace(req.ConversacionID)
	if convID == "" {
		convID = generarIDConversacion()
	}

	var historialPrevio []domain.MensajeChat
	if s.store != nil {
		historialPrevio = s.store.Obtener(req.UsuarioID, convID)
	}

	mensajes := make([]domain.MensajeChat, len(historialPrevio), len(historialPrevio)+1)
	copy(mensajes, historialPrevio)
	mensajes = append(mensajes, domain.MensajeChat{
		Rol:   domain.RolUsuario,
		Texto: req.Mensaje,
	})

	toolDefs := s.registry.Definitions()
	acciones := make([]domain.AccionAgente, 0)

	for paso := 0; paso < s.maxIteraciones; paso++ {
		if err := ctx.Err(); err != nil {
			return domain.ChatResponse{}, err
		}

		inicio := time.Now()
		log.Printf("[Agente] Conv=%s Paso %d: consultando modelo de IA...", convID, paso+1)

		turno, err := s.llm.GenerarTurno(ctx, mensajes, toolDefs)
		if err != nil {
			return domain.ChatResponse{}, fmt.Errorf("%w: %w", domain.ErrProveedorIA, err)
		}

		log.Printf("[Agente] Conv=%s Paso %d respondido en %v (tools solicitadas: %d)", convID, paso+1, time.Since(inicio).Round(time.Millisecond), len(turno.ToolCalls))

		mensajes = append(mensajes, domain.MensajeChat{
			Rol:       domain.RolModelo,
			Texto:     turno.Text,
			ToolCalls: turno.ToolCalls,
		})

		if len(turno.ToolCalls) == 0 {
			if s.store != nil {
				s.store.Guardar(req.UsuarioID, convID, mensajes)
			}
			return domain.ChatResponse{
				ConversacionID: convID,
				Respuesta:      turno.Text,
				Acciones:       acciones,
			}, nil
		}

		resultados := s.ejecutarTools(ctx, req.UsuarioID, turno.ToolCalls)
		for i := range resultados {
			res := resultados[i]
			if !res.IsError && res.Name == "crear_reserva" {
				if reserva, ok := res.Content.(domain.ReservaCreada); ok && reserva.ID != "" {
					acciones = append(acciones, domain.AccionAgente{
						Tipo: "sesion.creada",
						ID:   reserva.ID,
					})
				}
			}
			mensajes = append(mensajes, domain.MensajeChat{
				Rol:        domain.RolHerramienta,
				ToolResult: &res,
			})
		}
	}

	return domain.ChatResponse{}, domain.ErrMaxIteraciones
}

// ejecutarTools usa fast-path secuencial para 1 tool y Fan-Out/Fan-In paralelo sin locks para N tools.
func (s *AgentService) ejecutarTools(ctx context.Context, usuarioID string, calls []domain.ToolCall) []domain.ToolResult {
	if len(calls) == 1 {
		return []domain.ToolResult{s.ejecutarUnaTool(ctx, usuarioID, calls[0])}
	}

	resultados := make([]domain.ToolResult, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i, call := range calls {
		go func(idx int, tc domain.ToolCall) {
			defer wg.Done()
			resultados[idx] = s.ejecutarUnaTool(ctx, usuarioID, tc)
		}(i, call)
	}

	wg.Wait()
	return resultados
}

// ejecutarUnaTool resuelve la herramienta en O(1) y encapsula errores para autocorrección del modelo.
func (s *AgentService) ejecutarUnaTool(ctx context.Context, usuarioID string, call domain.ToolCall) domain.ToolResult {
	log.Printf("[Agente] Ejecutando tool '%s' con args=%v", call.Name, call.Arguments)

	tool, ok := s.registry.Get(call.Name)
	if !ok {
		return domain.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: map[string]string{"error": domain.ErrToolDesconocida.Error()},
			IsError: true,
		}
	}

	salida, err := tool.Execute(ctx, usuarioID, call.Arguments)
	if err != nil {
		return domain.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: map[string]string{"error": err.Error()},
			IsError: true,
		}
	}

	return domain.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: salida,
	}
}

func generarIDConversacion() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("conv-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

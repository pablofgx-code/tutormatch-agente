package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

const defaultMaxIteraciones = 6

// AgentService orquesta el ciclo de conversación entre el modelo de IA y las tools MCP.
type AgentService struct {
	llm            domain.LLMProvider
	registry       domain.ToolRegistry
	maxIteraciones int
}

func NewAgentService(llm domain.LLMProvider, registry domain.ToolRegistry, maxIteraciones int) *AgentService {
	if maxIteraciones <= 0 {
		maxIteraciones = defaultMaxIteraciones
	}
	return &AgentService{
		llm:            llm,
		registry:       registry,
		maxIteraciones: maxIteraciones,
	}
}

// ProcesarChat ejecuta el loop del agente hasta obtener una respuesta en texto o agotar iteraciones.
func (s *AgentService) ProcesarChat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error) {
	if err := req.Validar(); err != nil {
		return domain.ChatResponse{}, err
	}

	// Copiamos el historial para no mutar el slice original y agregamos el mensaje actual.
	mensajes := make([]domain.MensajeChat, len(req.Historial), len(req.Historial)+1)
	copy(mensajes, req.Historial)
	mensajes = append(mensajes, domain.MensajeChat{
		Rol:   domain.RolUsuario,
		Texto: req.Mensaje,
	})

	// Obtenemos las definiciones una sola vez fuera del loop (evita reconstrucciones repetidas).
	toolDefs := s.registry.Definitions()

	for paso := 0; paso < s.maxIteraciones; paso++ {
		if err := ctx.Err(); err != nil {
			return domain.ChatResponse{}, err
		}

		inicio := time.Now()
		log.Printf("[Agente] Paso %d: consultando modelo de IA...", paso+1)

		turno, err := s.llm.GenerarTurno(ctx, mensajes, toolDefs)
		if err != nil {
			return domain.ChatResponse{}, fmt.Errorf("%w: %w", domain.ErrProveedorIA, err)
		}

		log.Printf("[Agente] Paso %d respondido en %v (tools solicitadas: %d)", paso+1, time.Since(inicio).Round(time.Millisecond), len(turno.ToolCalls))

		mensajes = append(mensajes, domain.MensajeChat{
			Rol:       domain.RolModelo,
			Texto:     turno.Text,
			ToolCalls: turno.ToolCalls,
		})

		// Condición de salida: el modelo ya tiene la respuesta final y no solicita más tools.
		if len(turno.ToolCalls) == 0 {
			return domain.ChatResponse{Respuesta: turno.Text}, nil
		}

		resultados := s.ejecutarTools(ctx, req.UsuarioID, turno.ToolCalls)
		for i := range resultados {
			res := resultados[i]
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

// ejecutarUnaTool resuelve la herramienta en O(1) y encapsula errores para que el modelo pueda recuperarse.
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

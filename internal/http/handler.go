package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/tutormatch/agente/internal/domain"
)

const maxRequestBodyBytes = 1 << 20 // 1 MB

// ChatProcessor desacopla el handler HTTP de la implementación concreta del servicio (DIP).
type ChatProcessor interface {
	ProcesarChat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error)
}

type Handler struct {
	chat     ChatProcessor
	registry domain.ToolRegistry
	limiter  RateLimiter
}

func NewHandler(chat ChatProcessor, registry domain.ToolRegistry, limiter RateLimiter) *Handler {
	return &Handler{
		chat:     chat,
		registry: registry,
		limiter:  limiter,
	}
}

// Routes registra las rutas REST, MCP y healthcheck envueltas con cabeceras de seguridad.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("POST /agent/chat", h.handleAgentChat)
	mux.HandleFunc("POST /mcp", h.handleMCP)
	return conCabecerasSeguridad(mux)
}

func conCabecerasSeguridad(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	escribirJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	var req domain.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		escribirError(w, http.StatusBadRequest, "cuerpo JSON inválido")
		return
	}

	if !h.permitirPeticion(claveLimite("chat", req.UsuarioID, r)) {
		w.Header().Set("Retry-After", "6")
		escribirError(w, http.StatusTooManyRequests, "demasiadas solicitudes; intenta nuevamente en unos segundos")
		return
	}

	ctx := domain.ConAuthToken(r.Context(), r.Header.Get("Authorization"))

	resp, err := h.chat.ProcesarChat(ctx, req)
	if err != nil {
		h.responderErrorDominio(w, err)
		return
	}

	escribirJSON(w, http.StatusOK, resp)
}

// --- Protocolo MCP (JSON-RPC 2.0 sobre HTTP) ---

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (h *Handler) handleMCP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	if !h.permitirPeticion(claveLimite("mcp", r.Header.Get("X-Usuario-Id"), r)) {
		w.Header().Set("Retry-After", "6")
		escribirJSON(w, http.StatusTooManyRequests, mcpResponse{
			JSONRPC: "2.0",
			Error:   &mcpRPCError{Code: -32000, Message: "demasiadas solicitudes MCP"},
		})
		return
	}

	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		escribirJSON(w, http.StatusBadRequest, mcpResponse{
			JSONRPC: "2.0",
			Error:   &mcpRPCError{Code: -32700, Message: "error de parseo JSON-RPC"},
		})
		return
	}

	switch req.Method {
	case "initialize":
		escribirJSON(w, http.StatusOK, mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo": map[string]string{
					"name":    "tutormatch-mcp-server",
					"version": "1.0.0",
				},
			},
		})

	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)

	case "ping":
		escribirJSON(w, http.StatusOK, mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		})

	case "tools/list":
		defs := h.registry.Definitions()
		mcpTools := make([]map[string]any, 0, len(defs))
		for _, d := range defs {
			mcpTools = append(mcpTools, map[string]any{
				"name":        d.Name,
				"description": d.Description,
				"inputSchema": d.Parameters,
			})
		}
		escribirJSON(w, http.StatusOK, mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": mcpTools},
		})

	case "tools/call":
		h.handleMCPToolCall(w, r, req)

	default:
		escribirJSON(w, http.StatusOK, mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcpRPCError{Code: -32601, Message: "método MCP no soportado"},
		})
	}
}

func (h *Handler) handleMCPToolCall(w http.ResponseWriter, r *http.Request, req mcpRequest) {
	var params mcpCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		escribirJSON(w, http.StatusOK, mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcpRPCError{Code: -32602, Message: "parámetros inválidos en tools/call"},
		})
		return
	}

	tool, ok := h.registry.Get(params.Name)
	if !ok {
		escribirJSON(w, http.StatusOK, mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcpRPCError{Code: -32602, Message: domain.ErrToolDesconocida.Error()},
		})
		return
	}

	ctx := domain.ConAuthToken(r.Context(), r.Header.Get("Authorization"))
	usuarioID := strings.TrimSpace(r.Header.Get("X-Usuario-Id"))

	salida, err := tool.Execute(ctx, usuarioID, params.Arguments)
	if err != nil {
		escribirJSON(w, http.StatusOK, mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]string{{"type": "text", "text": err.Error()}},
				"isError": true,
			},
		})
		return
	}

	textoJSON, _ := json.Marshal(salida)
	escribirJSON(w, http.StatusOK, mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]string{{"type": "text", "text": string(textoJSON)}},
			"isError": false,
		},
	})
}

func (h *Handler) permitirPeticion(clave string) bool {
	if h.limiter == nil {
		return true
	}
	return h.limiter.Allow(clave)
}

func claveLimite(prefijo, usuarioID string, r *http.Request) string {
	if id := strings.TrimSpace(usuarioID); id != "" {
		return prefijo + ":user:" + id
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil || host == "" {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	return prefijo + ":ip:" + host
}

// responderErrorDominio registra el detalle técnico en logs y devuelve mensajes saneados al cliente.
func (h *Handler) responderErrorDominio(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrArgumentoInvalido):
		escribirError(w, http.StatusBadRequest, domain.ErrArgumentoInvalido.Error())
	case errors.Is(err, domain.ErrProveedorIA):
		log.Printf("[HTTP] Error proveedor IA: %v", err)
		escribirError(w, http.StatusServiceUnavailable, "el servicio de IA no está disponible temporalmente")
	case errors.Is(err, domain.ErrMaxIteraciones):
		log.Printf("[HTTP] Máximo de iteraciones alcanzado: %v", err)
		escribirError(w, http.StatusGatewayTimeout, domain.ErrMaxIteraciones.Error())
	default:
		log.Printf("[HTTP] Error interno inesperado: %v", err)
		escribirError(w, http.StatusInternalServerError, "error interno del servidor")
	}
}

func escribirJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func escribirError(w http.ResponseWriter, status int, mensaje string) {
	escribirJSON(w, status, map[string]string{"error": mensaje})
}

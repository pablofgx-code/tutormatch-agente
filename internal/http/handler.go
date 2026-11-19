package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
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
}

func NewHandler(chat ChatProcessor, registry domain.ToolRegistry) *Handler {
	return &Handler{
		chat:     chat,
		registry: registry,
	}
}

// Routes registra las rutas REST, MCP y healthcheck usando el enrutador estándar de Go.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("POST /agent/chat", h.handleAgentChat)
	mux.HandleFunc("POST /mcp", h.handleMCP)
	return mux
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

	resp, err := h.chat.ProcesarChat(r.Context(), req)
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
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]string{
					"name":    "tutormatch-mcp-server",
					"version": "1.0.0",
				},
			},
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

	usuarioID := strings.TrimSpace(r.Header.Get("X-Usuario-Id"))
	salida, err := tool.Execute(r.Context(), usuarioID, params.Arguments)
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

func (h *Handler) responderErrorDominio(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrArgumentoInvalido):
		escribirError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrProveedorIA):
		escribirError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, domain.ErrMaxIteraciones):
		escribirError(w, http.StatusGatewayTimeout, err.Error())
	default:
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

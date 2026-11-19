package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tutormatch/agente/internal/adapter"
	"github.com/tutormatch/agente/internal/domain"
	"github.com/tutormatch/agente/internal/mcp"
)

type stubChatProcessor struct {
	resp domain.ChatResponse
	err  error
}

func (s *stubChatProcessor) ProcesarChat(_ context.Context, _ domain.ChatRequest) (domain.ChatResponse, error) {
	return s.resp, s.err
}

func TestHandler_Health(t *testing.T) {
	h := NewHandler(&stubChatProcessor{}, mcp.NewDefaultRegistry(adapter.NewMockBFFClient()))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "charset=utf-8") {
		t.Fatalf("Content-Type sin utf-8: %q", rec.Header().Get("Content-Type"))
	}
}

func TestHandler_AgentChat(t *testing.T) {
	reg := mcp.NewDefaultRegistry(adapter.NewMockBFFClient())

	t.Run("respuesta exitosa", func(t *testing.T) {
		h := NewHandler(&stubChatProcessor{resp: domain.ChatResponse{Respuesta: "Listo"}}, reg)
		req := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"usuario_id":"u1","mensaje":"hola"}`))
		rec := httptest.NewRecorder()

		h.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("esperaba 200, obtuve %d", rec.Code)
		}
		var out domain.ChatResponse
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil || out.Respuesta != "Listo" {
			t.Fatalf("respuesta inesperada: %+v err=%v", out, err)
		}
	})

	t.Run("json invalido", func(t *testing.T) {
		h := NewHandler(&stubChatProcessor{}, reg)
		req := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{roto`))
		rec := httptest.NewRecorder()

		h.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("esperaba 400, obtuve %d", rec.Code)
		}
	})

	casosError := []struct {
		nombre     string
		errDominio error
		statusEsp  int
	}{
		{"argumento invalido", domain.ErrArgumentoInvalido, http.StatusBadRequest},
		{"error proveedor ia", domain.ErrProveedorIA, http.StatusBadGateway},
		{"max iteraciones", domain.ErrMaxIteraciones, http.StatusGatewayTimeout},
		{"error generico", errors.New("fallo inesperado"), http.StatusInternalServerError},
	}

	for _, tc := range casosError {
		t.Run(tc.nombre, func(t *testing.T) {
			h := NewHandler(&stubChatProcessor{err: tc.errDominio}, reg)
			req := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"usuario_id":"u1","mensaje":"hola"}`))
			rec := httptest.NewRecorder()

			h.Routes().ServeHTTP(rec, req)
			if rec.Code != tc.statusEsp {
				t.Fatalf("esperaba %d, obtuve %d", tc.statusEsp, rec.Code)
			}
		})
	}
}

func TestHandler_MCP(t *testing.T) {
	h := NewHandler(&stubChatProcessor{}, mcp.NewDefaultRegistry(adapter.NewMockBFFClient()))
	router := h.Routes()

	ejecutarMCP := func(body string, usuarioID string) (*httptest.ResponseRecorder, mcpResponse) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
		if usuarioID != "" {
			req.Header.Set("X-Usuario-Id", usuarioID)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var resp mcpResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		return rec, resp
	}

	// 1. JSON inválido
	rec, resp := ejecutarMCP(`{roto`, "")
	if rec.Code != http.StatusBadRequest || resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("esperaba error -32700, obtuve status=%d resp=%+v", rec.Code, resp)
	}

	// 2. initialize
	rec, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`, "")
	if rec.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("initialize falló: %+v", resp)
	}

	// 3. tools/list
	rec, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "")
	if rec.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("tools/list falló: %+v", resp)
	}

	// 4. tools/call exitoso
	rec, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"buscar_tutores","arguments":{"ramo_codigo":"MAT1620"}}}`, "u-1")
	if rec.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("tools/call exitoso falló: %+v", resp)
	}

	// 5. tools/call con error de validación de tool (isError: true)
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"buscar_tutores","arguments":{}}}`, "u-1")
	resMap, ok := resp.Result.(map[string]any)
	if !ok || resMap["isError"] != true {
		t.Fatalf("se esperaba isError=true, obtuve %+v", resp)
	}

	// 6. tools/call con tool desconocida
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"no_existe","arguments":{}}}`, "")
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("se esperaba error -32602, obtuve %+v", resp)
	}

	// 7. tools/call con params inválidos
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":"invalido"}`, "")
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("se esperaba error -32602 para params inválidos, obtuve %+v", resp)
	}

	// 8. método no soportado
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":7,"method":"desconocido"}`, "")
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("se esperaba error -32601, obtuve %+v", resp)
	}
}

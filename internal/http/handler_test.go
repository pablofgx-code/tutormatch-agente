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
	"time"

	"github.com/tutormatch/agente/internal/adapter"
	"github.com/tutormatch/agente/internal/domain"
	"github.com/tutormatch/agente/internal/mcp"
)

type stubChatProcessor struct {
	resp      domain.ChatResponse
	err       error
	authVisto string
}

func (s *stubChatProcessor) ProcesarChat(ctx context.Context, _ domain.ChatRequest) (domain.ChatResponse, error) {
	s.authVisto = domain.AuthTokenDesdeContexto(ctx)
	return s.resp, s.err
}

func TestTokenBucketLimiter_RafagaRecargaYEviccion(t *testing.T) {
	ahora := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	lim := NewTokenBucketLimiter(60, time.Minute, 2, 2) // 1 req/s, burst 2, max 2 claves
	lim.now = func() time.Time { return ahora }

	if !lim.Allow("u1") {
		t.Fatal("1ra petición de ráfaga debía permitirse")
	}
	if !lim.Allow("u1") {
		t.Fatal("2da petición de ráfaga debía permitirse")
	}
	if lim.Allow("u1") {
		t.Fatal("la 3ra petición inmediata debía ser rechazada")
	}

	// Tras 1 segundo se recarga 1 token
	ahora = ahora.Add(time.Second)
	if !lim.Allow("u1") {
		t.Fatal("tras 1s debía permitir 1 petición")
	}

	// Evicción LRU cuando supera maxKeys
	lim.Allow("u2")
	lim.Allow("u3")
	if len(lim.buckets) != 2 {
		t.Fatalf("esperaba 2 buckets tras evicción LRU, obtuve %d", len(lim.buckets))
	}
}

func TestHandler_HealthYCabecerasSeguridad(t *testing.T) {
	h := NewHandler(&stubChatProcessor{}, mcp.NewDefaultRegistry(adapter.NewMockBFFClient()), nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d", rec.Code)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("faltan cabeceras defensivas: %+v", rec.Header())
	}
}

func TestHandler_AgentChatAuthErroresYRateLimit(t *testing.T) {
	reg := mcp.NewDefaultRegistry(adapter.NewMockBFFClient())

	t.Run("respuesta exitosa reenviando JWT", func(t *testing.T) {
		stub := &stubChatProcessor{
			resp: domain.ChatResponse{
				ConversacionID: "conv-1",
				Respuesta:      "Listo",
				Acciones:       []domain.AccionAgente{{Tipo: "sesion.creada", ID: "s-1"}},
			},
		}
		h := NewHandler(stub, reg, nil)
		req := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"usuarioId":"u1","mensaje":"hola"}`))
		req.Header.Set("Authorization", "Bearer jwt-prueba")
		rec := httptest.NewRecorder()

		h.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || stub.authVisto != "Bearer jwt-prueba" {
			t.Fatalf("status=%d auth=%q", rec.Code, stub.authVisto)
		}
		var out domain.ChatResponse
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil || out.ConversacionID != "conv-1" || len(out.Acciones) != 1 {
			t.Fatalf("respuesta inesperada: %+v err=%v", out, err)
		}
	})

	t.Run("rate limit 429", func(t *testing.T) {
		lim := NewTokenBucketLimiter(1, time.Minute, 1, 10)
		h := NewHandler(&stubChatProcessor{resp: domain.ChatResponse{Respuesta: "ok"}}, reg, lim)
		router := h.Routes()

		req1 := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"usuarioId":"u1","mensaje":"hola"}`))
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)
		if rec1.Code != http.StatusOK {
			t.Fatalf("1ra petición esperaba 200, obtuve %d", rec1.Code)
		}

		req2 := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"usuarioId":"u1","mensaje":"hola"}`))
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusTooManyRequests {
			t.Fatalf("2da petición esperaba 429, obtuve %d", rec2.Code)
		}
	})

	t.Run("json invalido", func(t *testing.T) {
		h := NewHandler(&stubChatProcessor{}, reg, nil)
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
		{"error proveedor ia saneado 503", domain.ErrProveedorIA, http.StatusServiceUnavailable},
		{"max iteraciones", domain.ErrMaxIteraciones, http.StatusGatewayTimeout},
		{"error generico", errors.New("fallo inesperado"), http.StatusInternalServerError},
	}

	for _, tc := range casosError {
		t.Run(tc.nombre, func(t *testing.T) {
			h := NewHandler(&stubChatProcessor{err: tc.errDominio}, reg, nil)
			req := httptest.NewRequest(http.MethodPost, "/agent/chat", bytes.NewBufferString(`{"usuarioId":"u1","mensaje":"hola"}`))
			rec := httptest.NewRecorder()
			h.Routes().ServeHTTP(rec, req)
			if rec.Code != tc.statusEsp {
				t.Fatalf("esperaba %d, obtuve %d", tc.statusEsp, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "fallo inesperado") {
				t.Fatalf("se filtró detalle interno en respuesta HTTP: %s", rec.Body.String())
			}
		})
	}
}

func TestHandler_MCP(t *testing.T) {
	h := NewHandler(&stubChatProcessor{}, mcp.NewDefaultRegistry(adapter.NewMockBFFClient()), nil)
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
		t.Fatalf("esperaba -32700, obtuve status=%d resp=%+v", rec.Code, resp)
	}

	// 2. initialize, notifications/initialized y ping
	rec, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`, "")
	if rec.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("initialize falló: %+v", resp)
	}
	rec, _ = ejecutarMCP(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("notifications/initialized esperaba 202, obtuve %d", rec.Code)
	}
	rec, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":2,"method":"ping"}`, "")
	if rec.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("ping falló: %+v", resp)
	}

	// 3. tools/list y tools/call
	rec, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, "")
	if rec.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("tools/list falló: %+v", resp)
	}
	rec, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"buscar_tutores","arguments":{"ramo_codigo":"MAT1620"}}}`, "u-1")
	if rec.Code != http.StatusOK || resp.Error != nil {
		t.Fatalf("tools/call falló: %+v", resp)
	}
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"buscar_tutores","arguments":{}}}`, "u-1")
	if resMap, ok := resp.Result.(map[string]any); !ok || resMap["isError"] != true {
		t.Fatalf("esperaba isError=true, obtuve %+v", resp)
	}
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"no_existe","arguments":{}}}`, "")
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("esperaba -32602, obtuve %+v", resp)
	}
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":"invalido"}`, "")
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("esperaba -32602 para params inválidos, obtuve %+v", resp)
	}
	_, resp = ejecutarMCP(`{"jsonrpc":"2.0","id":8,"method":"desconocido"}`, "")
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("esperaba -32601, obtuve %+v", resp)
	}

	// 4. Rate limit en /mcp
	hLim := NewHandler(&stubChatProcessor{}, mcp.NewDefaultRegistry(adapter.NewMockBFFClient()), NewTokenBucketLimiter(1, time.Minute, 1, 10))
	rLim := hLim.Routes()
	rLim.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)))
	recLim := httptest.NewRecorder()
	rLim.ServeHTTP(recLim, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)))
	if recLim.Code != http.StatusTooManyRequests {
		t.Fatalf("esperaba 429 en /mcp, obtuve %d", recLim.Code)
	}
}

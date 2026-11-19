package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tutormatch/agente/internal/domain"
)

func TestMockBFFClient_OperacionesYConcurrencia(t *testing.T) {
	mock := NewMockBFFClient()
	ctx := context.Background()

	tutores, err := mock.BuscarTutores(ctx, "MAT1620")
	if err != nil || len(tutores) != 2 {
		t.Fatalf("esperaba 2 tutores, obtuve len=%d err=%v", len(tutores), err)
	}

	sinTutores, _ := mock.BuscarTutores(ctx, "RAMO9999")
	if len(sinTutores) != 0 {
		t.Fatalf("esperaba 0 tutores para ramo inexistente, obtuve %d", len(sinTutores))
	}

	todosBloques, _ := mock.VerDisponibilidad(ctx, "11111111-1111-1111-1111-111111111111", "")
	if len(todosBloques) != 2 {
		t.Fatalf("esperaba 2 bloques sin filtro de día, obtuve %d", len(todosBloques))
	}

	juevesBloques, _ := mock.VerDisponibilidad(ctx, "11111111-1111-1111-1111-111111111111", "jueves")
	if len(juevesBloques) != 1 {
		t.Fatalf("esperaba 1 bloque el jueves, obtuve %d", len(juevesBloques))
	}

	quiz, err := mock.GenerarQuiz(ctx, "", "MAT1620")
	if err != nil || len(quiz.Preguntas) != 2 {
		t.Fatalf("quiz inesperado: %+v err=%v", quiz, err)
	}

	// Verifica seguridad concurrente (útil al correr go test -race)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mock.CrearReserva(ctx, "t-1", "est-1", "MAT1620", "2026-11-26T15:00:00Z")
		}()
	}
	wg.Wait()
}

func TestHTTPBFFClient_ConsultasYMutaciones(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "tutoresPorRamo"):
			_, _ = w.Write([]byte(`{"data":{"tutoresPorRamo":[{"id":"t1","nombre":"Ana","email":"ana@ucn.cl","ramos":["MAT1620"]}]}}`))
		case strings.Contains(req.Query, "disponibilidadTutor"):
			_, _ = w.Write([]byte(`{"data":{"disponibilidadTutor":[{"id":"d1","tutor_id":"t1","dia":"jueves","hora_ini":"15:00","hora_fin":"16:00"}]}}`))
		case strings.Contains(req.Query, "crearReserva"):
			_, _ = w.Write([]byte(`{"data":{"crearReserva":{"sesion_id":"s1","tutor_id":"t1","estudiante_id":"e1","ramo_codigo":"MAT1620","estado":"PENDIENTE"}}}`))
		case strings.Contains(req.Query, "generarQuiz"):
			_, _ = w.Write([]byte(`{"data":{"generarQuiz":{"ramo_codigo":"MAT1620","preguntas":[{"pregunta":"P1","opciones":["A","B"],"respuesta_correcta":"A"}]}}}`))
		default:
			http.Error(w, "query desconocida", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client := NewHTTPBFFClient(srv.URL, srv.Client())
	ctx := context.Background()

	tutores, err := client.BuscarTutores(ctx, "MAT1620")
	if err != nil || len(tutores) != 1 || tutores[0].Nombre != "Ana" {
		t.Fatalf("BuscarTutores inesperado: %+v err=%v", tutores, err)
	}

	bloques, err := client.VerDisponibilidad(ctx, "t1", "jueves")
	if err != nil || len(bloques) != 1 || bloques[0].ID != "d1" {
		t.Fatalf("VerDisponibilidad inesperado: %+v err=%v", bloques, err)
	}

	reserva, err := client.CrearReserva(ctx, "t1", "e1", "MAT1620", "2026-11-26T15:00:00Z")
	if err != nil || reserva.SesionID != "s1" || reserva.Estado != "PENDIENTE" {
		t.Fatalf("CrearReserva inesperado: %+v err=%v", reserva, err)
	}

	quiz, err := client.GenerarQuiz(ctx, "s1", "MAT1620")
	if err != nil || len(quiz.Preguntas) != 1 {
		t.Fatalf("GenerarQuiz inesperado: %+v err=%v", quiz, err)
	}
}

func TestHTTPBFFClient_Errores(t *testing.T) {
	casos := []struct {
		nombre string
		status int
		body   string
	}{
		{"http 500", http.StatusInternalServerError, `error`},
		{"json invalido", http.StatusOK, `{no-es-json`},
		{"error graphql", http.StatusOK, `{"errors":[{"message":"no autorizado"}]}`},
		{"data invalida", http.StatusOK, `{"data":"invalido"}`},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			client := NewHTTPBFFClient(srv.URL, srv.Client())
			_, err := client.BuscarTutores(context.Background(), "MAT1620")
			if !errors.Is(err, domain.ErrServicioExterno) {
				t.Fatalf("esperaba ErrServicioExterno, obtuve %v", err)
			}
		})
	}
}

func TestGeminiProvider_ExitoAgrupacionYFirma(t *testing.T) {
	var recibido geminiRequest
	var apiKeyHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeyHeader = r.Header.Get("x-goog-api-key")
		if err := json.NewDecoder(r.Body).Decode(&recibido); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {
					"role": "model",
					"parts": [
						{"text": "razonamiento interno", "thought": true},
						{"text": "Consultando horarios..."},
						{"thoughtSignature": "sig-xyz", "functionCall": {"name": "ver_disponibilidad", "args": {"tutor_id": "t1"}}}
					]
				}
			}]
		}`))
	}))
	defer srv.Close()

	// Sin API Key debe fallar rápido sin hacer llamadas HTTP
	sinKey := NewGeminiProvider("", "", srv.Client()).WithBaseURL(srv.URL)
	if _, err := sinKey.GenerarTurno(context.Background(), nil, nil); err == nil {
		t.Fatal("se esperaba error cuando GEMINI_API_KEY está vacía")
	}

	provider := NewGeminiProvider("mi-api-key", "", srv.Client()).WithBaseURL(srv.URL)
	mensajes := []domain.MensajeChat{
		{Rol: domain.RolUsuario, Texto: "Hola"},
		{
			Rol: domain.RolModelo,
			ToolCalls: []domain.ToolCall{
				{ID: "c1", Name: "buscar_tutores", Arguments: map[string]any{"ramo_codigo": "MAT1620"}, Signature: "sig-prev"},
			},
		},
		// Dos resultados consecutivos deben agruparse en un solo turno "user"
		{Rol: domain.RolHerramienta, ToolResult: &domain.ToolResult{CallID: "c1", Name: "ver_disponibilidad", Content: "ok1"}},
		{Rol: domain.RolHerramienta, ToolResult: &domain.ToolResult{CallID: "c2", Name: "ver_disponibilidad", Content: "ok2"}},
	}
	tools := []domain.ToolDefinition{
		{Name: "buscar_tutores", Description: "Busca tutores", Parameters: map[string]any{"type": "object"}},
	}

	turno, err := provider.GenerarTurno(context.Background(), mensajes, tools)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if apiKeyHeader != "mi-api-key" {
		t.Fatalf("header x-goog-api-key esperado 'mi-api-key', obtuve %q", apiKeyHeader)
	}
	// Verifica agrupación de las 2 respuestas paralelas en contents[2]
	if len(recibido.Contents) != 3 || len(recibido.Contents[2].Parts) != 2 {
		t.Fatalf("las respuestas paralelas no se agruparon correctamente: %+v", recibido.Contents)
	}
	// Verifica reenvío de thoughtSignature en el turno del modelo
	if recibido.Contents[1].Parts[0].ThoughtSignature != "sig-prev" {
		t.Fatalf("no se reenvió thoughtSignature, obtuve %q", recibido.Contents[1].Parts[0].ThoughtSignature)
	}
	// Verifica que se filtró thought:true y se capturó la firma nueva
	if turno.Text != "Consultando horarios..." {
		t.Fatalf("texto inesperado: %q", turno.Text)
	}
	if len(turno.ToolCalls) != 1 || turno.ToolCalls[0].Signature != "sig-xyz" {
		t.Fatalf("tool calls inesperadas: %+v", turno.ToolCalls)
	}
}

func TestGeminiProvider_ErroresAPI(t *testing.T) {
	casos := []struct {
		nombre string
		status int
		body   string
	}{
		{"error api estructurado", http.StatusBadRequest, `{"error":{"code":400,"message":"bad request"}}`},
		{"error http sin detalle", http.StatusServiceUnavailable, `{}`},
		{"json invalido", http.StatusOK, `{invalido`},
		{"sin candidatos", http.StatusOK, `{"candidates":[]}`},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			provider := NewGeminiProvider("clave", "gemini-flash-lite-latest", srv.Client()).WithBaseURL(srv.URL)
			_, err := provider.GenerarTurno(context.Background(), []domain.MensajeChat{{Rol: domain.RolUsuario, Texto: "hola"}}, nil)
			if err == nil {
				t.Fatal("se esperaba error de GeminiProvider")
			}
		})
	}
}

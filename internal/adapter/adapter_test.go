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
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

func TestMockBFFClient_OperacionesYConcurrencia(t *testing.T) {
	mock := NewMockBFFClient()
	ctx := context.Background()

	ramos, err := mock.ListarRamos(ctx)
	if err != nil || len(ramos) < 3 {
		t.Fatalf("ListarRamos inesperado: len=%d err=%v", len(ramos), err)
	}

	tutores, err := mock.BuscarTutores(ctx, "MAT1620")
	if err != nil || len(tutores) != 2 {
		t.Fatalf("esperaba 2 tutores, obtuve len=%d err=%v", len(tutores), err)
	}
	if sinTutores, _ := mock.BuscarTutores(ctx, "RAMO9999"); len(sinTutores) != 0 {
		t.Fatalf("esperaba 0 tutores, obtuve %d", len(sinTutores))
	}

	desde := time.Now().UTC().Add(-time.Hour)
	hasta := time.Now().UTC().AddDate(0, 0, 14)

	libres, err := mock.BuscarDisponibilidad(ctx, "MAT1620", desde, hasta)
	if err != nil || len(libres) != 3 {
		t.Fatalf("esperaba 3 bloques libres para MAT1620, obtuve %d err=%v", len(libres), err)
	}

	deCamila, err := mock.VerDisponibilidad(ctx, "11111111-1111-1111-1111-111111111111", desde, hasta)
	if err != nil || len(deCamila) != 2 {
		t.Fatalf("esperaba 2 bloques de Camila, obtuve %d err=%v", len(deCamila), err)
	}

	// No permite reservarse a sí mismo
	if _, err := mock.CrearReserva(ctx, deCamila[0].TutorID, deCamila[0].TutorID, "MAT1620", deCamila[0].Inicio); !errors.Is(err, domain.ErrServicioExterno) {
		t.Fatalf("esperaba ErrServicioExterno al reservarse a sí mismo, obtuve %v", err)
	}

	// Reserva válida ocupa la casilla y la segunda reserva sobre la misma falla
	if _, err := mock.CrearReserva(ctx, deCamila[0].TutorID, "est-1", "MAT1620", deCamila[0].Inicio); err != nil {
		t.Fatalf("reserva válida falló: %v", err)
	}
	if _, err := mock.CrearReserva(ctx, deCamila[0].TutorID, "est-2", "MAT1620", deCamila[0].Inicio); !errors.Is(err, domain.ErrServicioExterno) {
		t.Fatalf("se esperaba rechazo en casilla ocupada, obtuve %v", err)
	}

	// Lecturas concurrentes (útil con -race)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mock.BuscarDisponibilidad(ctx, "MAT1620", desde, hasta)
		}()
	}
	wg.Wait()
}

type stubRamosBFF struct {
	domain.BFFClient
	llamadas int
	err      error
}

func (s *stubRamosBFF) ListarRamos(_ context.Context) ([]domain.Ramo, error) {
	s.llamadas++
	if s.err != nil {
		return nil, s.err
	}
	return []domain.Ramo{{Codigo: "MAT1620", Nombre: "Cálculo II"}}, nil
}

func TestCachedBFFClient_HitExpiracionYError(t *testing.T) {
	ahora := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	base := &stubRamosBFF{BFFClient: NewMockBFFClient()}
	cached := NewCachedBFFClient(base, 5*time.Minute)
	cached.now = func() time.Time { return ahora }

	ctx := context.Background()
	if _, err := cached.ListarRamos(ctx); err != nil {
		t.Fatalf("primera llamada falló: %v", err)
	}
	if _, err := cached.ListarRamos(ctx); err != nil {
		t.Fatalf("segunda llamada falló: %v", err)
	}
	if base.llamadas != 1 {
		t.Fatalf("esperaba 1 llamada (cache hit), obtuve %d", base.llamadas)
	}

	// Expira TTL y recarga
	ahora = ahora.Add(6 * time.Minute)
	if _, err := cached.ListarRamos(ctx); err != nil {
		t.Fatalf("recarga tras TTL falló: %v", err)
	}
	if base.llamadas != 2 {
		t.Fatalf("esperaba 2 llamadas tras expirar TTL, obtuve %d", base.llamadas)
	}

	// Errores no deben quedarse cacheados
	baseErr := &stubRamosBFF{err: errors.New("bff caido")}
	cachedErr := NewCachedBFFClient(baseErr, 0)
	if _, err := cachedErr.ListarRamos(ctx); err == nil {
		t.Fatal("se esperaba error propagado")
	}
}

func TestLRUConversationStore_EviccionTTLYRecorteSeguro(t *testing.T) {
	ahora := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	store := NewLRUConversationStore(2, 10*time.Minute, 4)
	store.now = func() time.Time { return ahora }

	store.Guardar("u1", "c1", []domain.MensajeChat{{Rol: domain.RolUsuario, Texto: "m1"}})
	store.Guardar("u1", "c2", []domain.MensajeChat{{Rol: domain.RolUsuario, Texto: "m2"}})
	// Tocar c1 lo vuelve más reciente; al insertar c3 debe expulsar c2
	if got := store.Obtener("u1", "c1"); len(got) != 1 {
		t.Fatalf("se esperaba historial c1, obtuve %+v", got)
	}
	store.Guardar("u1", "c3", []domain.MensajeChat{{Rol: domain.RolUsuario, Texto: "m3"}})
	if got := store.Obtener("u1", "c2"); got != nil {
		t.Fatalf("c2 debía ser expulsada por LRU, obtuve %+v", got)
	}

	// Actualizar entrada existente
	store.Guardar("u1", "c1", []domain.MensajeChat{
		{Rol: domain.RolUsuario, Texto: "t1"},
		{Rol: domain.RolModelo, ToolCalls: []domain.ToolCall{{ID: "1", Name: "listar_ramos"}}},
		{Rol: domain.RolHerramienta, ToolResult: &domain.ToolResult{CallID: "1", Name: "listar_ramos"}},
		{Rol: domain.RolModelo, Texto: "r1"},
		{Rol: domain.RolUsuario, Texto: "t2"},
		{Rol: domain.RolModelo, Texto: "r2"},
	})
	recortado := store.Obtener("u1", "c1")
	if len(recortado) == 0 || recortado[0].Rol != domain.RolUsuario {
		t.Fatalf("el historial recortado siempre debe empezar con RolUsuario, obtuve %+v", recortado)
	}

	// Expiración por TTL
	ahora = ahora.Add(11 * time.Minute)
	if got := store.Obtener("u1", "c1"); got != nil {
		t.Fatalf("c1 debía expirar por TTL, obtuve %+v", got)
	}
}

func TestHTTPBFFClient_ConsultasMutacionesYAuth(t *testing.T) {
	var authRecibido string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authRecibido = r.Header.Get("Authorization")
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "query ListarRamos"):
			_, _ = w.Write([]byte(`{"data":{"ramos":[{"codigo":"MAT1620","nombre":"Cálculo II"}]}}`))
		case strings.Contains(req.Query, "tutoresPorRamo"):
			_, _ = w.Write([]byte(`{"data":{"tutoresPorRamo":[{"id":"t1","nombre":"Ana","email":"ana@ucn.cl","promedioEvaluaciones":4.8,"cantidadEvaluaciones":5,"ramos":[{"activo":true,"ramo":{"codigo":"MAT1620","nombre":"Cálculo II"}}]}]}}`))
		case strings.Contains(req.Query, "buscarDisponibilidad"):
			_, _ = w.Write([]byte(`{"data":{"buscarDisponibilidad":[{"id":"d1","tutorId":"t1","ramoCodigo":"MAT1620","inicio":"2030-11-26T17:30:00Z","fin":"2030-11-26T19:00:00Z","libre":true}]}}`))
		case strings.Contains(req.Query, "disponibilidad("):
			_, _ = w.Write([]byte(`{"data":{"disponibilidad":[{"id":"d1","tutorId":"t1","ramoCodigo":"MAT1620","inicio":"2030-11-26T17:30:00Z","fin":"2030-11-26T19:00:00Z","libre":true}]}}`))
		case strings.Contains(req.Query, "crearSesion"):
			_, _ = w.Write([]byte(`{"data":{"crearSesion":{"id":"s1","tutorId":"t1","estudianteId":"e1","ramoCodigo":"MAT1620","inicio":"2030-11-26T17:30:00Z","fin":"2030-11-26T19:00:00Z","estado":"PENDIENTE","creadaEn":"2030-11-20T12:00:00Z"}}}`))
		default:
			http.Error(w, "query desconocida", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client := NewHTTPBFFClient(srv.URL, srv.Client())
	ctx := domain.ConAuthToken(context.Background(), "Bearer jwt-del-alumno")
	inicio := time.Date(2030, 11, 26, 17, 30, 0, 0, time.UTC)
	hasta := inicio.Add(24 * time.Hour)

	if ramos, err := client.ListarRamos(ctx); err != nil || len(ramos) != 1 {
		t.Fatalf("ListarRamos falló: %+v err=%v", ramos, err)
	}
	if tutores, err := client.BuscarTutores(ctx, "MAT1620"); err != nil || len(tutores) != 1 || tutores[0].PromedioEvaluaciones != 4.8 {
		t.Fatalf("BuscarTutores falló: %+v err=%v", tutores, err)
	}
	if bloques, err := client.BuscarDisponibilidad(ctx, "MAT1620", inicio, hasta); err != nil || len(bloques) != 1 {
		t.Fatalf("BuscarDisponibilidad falló: %+v err=%v", bloques, err)
	}
	if bloques, err := client.VerDisponibilidad(ctx, "t1", inicio, hasta); err != nil || len(bloques) != 1 {
		t.Fatalf("VerDisponibilidad falló: %+v err=%v", bloques, err)
	}

	// Sin token debe fallar antes de llamar al BFF
	if _, err := client.CrearReserva(context.Background(), "t1", "e1", "MAT1620", inicio); !errors.Is(err, domain.ErrServicioExterno) {
		t.Fatalf("esperaba ErrServicioExterno sin Authorization, obtuve %v", err)
	}

	reserva, err := client.CrearReserva(ctx, "t1", "e1", "MAT1620", inicio)
	if err != nil || reserva.ID != "s1" || authRecibido != "Bearer jwt-del-alumno" {
		t.Fatalf("CrearReserva falló: reserva=%+v auth=%q err=%v", reserva, authRecibido, err)
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

func TestGeminiProvider_ExitoReintentoYErrores(t *testing.T) {
	var recibido geminiRequest
	var apiKeyHeader string
	intentos := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intentos++
		apiKeyHeader = r.Header.Get("x-goog-api-key")
		// Primer intento simula 503 transitorio; el segundo responde OK.
		if intentos == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"message":"high demand"}}`))
			return
		}
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
						{"thoughtSignature": "sig-xyz", "functionCall": {"name": "buscar_disponibilidad", "args": {"ramo_codigo": "MAT1620"}}}
					]
				}
			}]
		}`))
	}))
	defer srv.Close()

	// Sin API Key debe fallar rápido
	sinKey := NewGeminiProvider("", "", srv.Client()).WithBaseURL(srv.URL)
	if _, err := sinKey.GenerarTurno(context.Background(), nil, nil); err == nil {
		t.Fatal("se esperaba error cuando GEMINI_API_KEY está vacía")
	}

	provider := NewGeminiProvider("mi-api-key", "", srv.Client()).WithBaseURL(srv.URL)
	provider.retryDelay = time.Millisecond

	mensajes := []domain.MensajeChat{
		{Rol: domain.RolUsuario, Texto: "Hola"},
		{
			Rol: domain.RolModelo,
			ToolCalls: []domain.ToolCall{
				{ID: "c1", Name: "buscar_tutores", Arguments: map[string]any{"ramo_codigo": "MAT1620"}, Signature: "sig-prev"},
			},
		},
		{Rol: domain.RolHerramienta, ToolResult: &domain.ToolResult{CallID: "c1", Name: "buscar_tutores", Content: "ok1"}},
		{Rol: domain.RolHerramienta, ToolResult: &domain.ToolResult{CallID: "c2", Name: "buscar_disponibilidad", Content: "ok2"}},
	}
	tools := []domain.ToolDefinition{
		{Name: "buscar_tutores", Description: "Busca tutores", Parameters: map[string]any{"type": "object"}},
	}

	turno, err := provider.GenerarTurno(context.Background(), mensajes, tools)
	if err != nil {
		t.Fatalf("error inesperado tras reintento: %v", err)
	}
	if intentos != 2 || apiKeyHeader != "mi-api-key" {
		t.Fatalf("esperaba 2 intentos y header correcto, intentos=%d key=%q", intentos, apiKeyHeader)
	}
	if len(recibido.Contents) != 3 || len(recibido.Contents[2].Parts) != 2 {
		t.Fatalf("respuestas paralelas mal agrupadas: %+v", recibido.Contents)
	}
	if recibido.Contents[1].Parts[0].ThoughtSignature != "sig-prev" {
		t.Fatalf("no se reenvió thoughtSignature")
	}
	if turno.Text != "Consultando horarios..." || len(turno.ToolCalls) != 1 || turno.ToolCalls[0].Signature != "sig-xyz" {
		t.Fatalf("turno inesperado: %+v", turno)
	}

	// Errores no recuperables
	casos := []struct {
		nombre string
		status int
		body   string
	}{
		{"error 400 estructurado", http.StatusBadRequest, `{"error":{"code":400,"message":"bad request"}}`},
		{"error 503 persistente", http.StatusServiceUnavailable, `{}`},
		{"json invalido", http.StatusOK, `{invalido`},
		{"sin candidatos", http.StatusOK, `{"candidates":[]}`},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer errSrv.Close()

			p := NewGeminiProvider("clave", "gemini-flash-lite-latest", errSrv.Client()).WithBaseURL(errSrv.URL)
			p.retryDelay = time.Millisecond
			if _, err := p.GenerarTurno(context.Background(), []domain.MensajeChat{{Rol: domain.RolUsuario, Texto: "hola"}}, nil); err == nil {
				t.Fatal("se esperaba error de GeminiProvider")
			}
		})
	}
}

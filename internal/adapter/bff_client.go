package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

const (
	defaultHTTPTimeout   = 10 * time.Second
	maxResponseBodyBytes = 2 << 20 // 2 MB de protección contra respuestas gigantes
)

// --- 1. Adaptador real HTTP/GraphQL hacia el BFF ---

type HTTPBFFClient struct {
	endpoint   string
	httpClient *http.Client
}

func NewHTTPBFFClient(endpoint string, httpClient *http.Client) *HTTPBFFClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &HTTPBFFClient{
		endpoint:   strings.TrimRight(endpoint, "/"),
		httpClient: httpClient,
	}
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphQLError  `json:"errors,omitempty"`
}

// ejecutarGraphQL centraliza (DRY) todas las consultas y mutaciones hacia el BFF.
func (c *HTTPBFFClient) ejecutarGraphQL(ctx context.Context, query string, variables map[string]any, destino any) error {
	payload, err := json.Marshal(graphQLRequest{
		Query:     query,
		Variables: variables,
	})
	if err != nil {
		return fmt.Errorf("%w: error serializando consulta GraphQL: %v", domain.ErrServicioExterno, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: error creando petición HTTP: %v", domain.ErrServicioExterno, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: error conectando con el BFF: %v", domain.ErrServicioExterno, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return fmt.Errorf("%w: error leyendo respuesta del BFF: %v", domain.ErrServicioExterno, err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: el BFF respondió HTTP %d", domain.ErrServicioExterno, resp.StatusCode)
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return fmt.Errorf("%w: respuesta GraphQL inválida: %v", domain.ErrServicioExterno, err)
	}
	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("%w: %s", domain.ErrServicioExterno, gqlResp.Errors[0].Message)
	}

	if err := json.Unmarshal(gqlResp.Data, destino); err != nil {
		return fmt.Errorf("%w: error decodificando datos del BFF: %v", domain.ErrServicioExterno, err)
	}
	return nil
}

func (c *HTTPBFFClient) BuscarTutores(ctx context.Context, ramoCodigo string) ([]domain.TutorResumen, error) {
	const query = `
		query BuscarTutores($ramoCodigo: String!) {
			tutoresPorRamo(ramoCodigo: $ramoCodigo) {
				id
				nombre
				email
				ramos
			}
		}`
	var out struct {
		TutoresPorRamo []domain.TutorResumen `json:"tutoresPorRamo"`
	}
	err := c.ejecutarGraphQL(ctx, query, map[string]any{"ramoCodigo": ramoCodigo}, &out)
	return out.TutoresPorRamo, err
}

func (c *HTTPBFFClient) VerDisponibilidad(ctx context.Context, tutorID, dia string) ([]domain.BloqueDisponibilidad, error) {
	const query = `
		query VerDisponibilidad($tutorId: String!, $dia: String) {
			disponibilidadTutor(tutorId: $tutorId, dia: $dia) {
				id
				tutor_id
				dia
				hora_ini
				hora_fin
			}
		}`
	var out struct {
		DisponibilidadTutor []domain.BloqueDisponibilidad `json:"disponibilidadTutor"`
	}
	err := c.ejecutarGraphQL(ctx, query, map[string]any{"tutorId": tutorID, "dia": dia}, &out)
	return out.DisponibilidadTutor, err
}

func (c *HTTPBFFClient) CrearReserva(ctx context.Context, tutorID, estudianteID, ramoCodigo, horario string) (domain.ReservaCreada, error) {
	const mutation = `
		mutation CrearReserva($tutorId: String!, $estudianteId: String!, $ramoCodigo: String!, $horario: String!) {
			crearReserva(tutorId: $tutorId, estudianteId: $estudianteId, ramoCodigo: $ramoCodigo, horario: $horario) {
				sesion_id
				tutor_id
				estudiante_id
				ramo_codigo
				estado
				creada_en
			}
		}`
	var out struct {
		CrearReserva domain.ReservaCreada `json:"crearReserva"`
	}
	vars := map[string]any{
		"tutorId":      tutorID,
		"estudianteId": estudianteID,
		"ramoCodigo":   ramoCodigo,
		"horario":      horario,
	}
	err := c.ejecutarGraphQL(ctx, mutation, vars, &out)
	return out.CrearReserva, err
}

func (c *HTTPBFFClient) GenerarQuiz(ctx context.Context, sesionID, ramoCodigo string) (domain.QuizResultado, error) {
	const query = `
		query GenerarQuiz($sesionId: String, $ramoCodigo: String!) {
			generarQuiz(sesionId: $sesionId, ramoCodigo: $ramoCodigo) {
				ramo_codigo
				preguntas {
					pregunta
					opciones
					respuesta_correcta
				}
			}
		}`
	var out struct {
		GenerarQuiz domain.QuizResultado `json:"generarQuiz"`
	}
	vars := map[string]any{
		"sesionId":   sesionID,
		"ramoCodigo": ramoCodigo,
	}
	err := c.ejecutarGraphQL(ctx, query, vars, &out)
	return out.GenerarQuiz, err
}

// --- 2. Adaptador Mock en memoria (para desarrollo y pruebas sin depender de otros servicios) ---

type MockBFFClient struct {
	mu             sync.RWMutex
	tutores        []domain.TutorResumen
	disponibilidad []domain.BloqueDisponibilidad
	reservas       []domain.ReservaCreada
	contadorID     int
}

func NewMockBFFClient() *MockBFFClient {
	return &MockBFFClient{
		tutores: []domain.TutorResumen{
			{
				ID:     "11111111-1111-1111-1111-111111111111",
				Nombre: "Camila Rojas",
				Email:  "camila.rojas@ucn.cl",
				Ramos:  []string{"MAT1610", "MAT1620"},
			},
			{
				ID:     "22222222-2222-2222-2222-222222222222",
				Nombre: "Matías Vega",
				Email:  "matias.vega@ucn.cl",
				Ramos:  []string{"MAT1620", "IIC1103", "IIC2133"},
			},
			{
				ID:     "33333333-3333-3333-3333-333333333333",
				Nombre: "Sofía Morales",
				Email:  "sofia.morales@ucn.cl",
				Ramos:  []string{"FIS1514", "MAT1610"},
			},
		},
		disponibilidad: []domain.BloqueDisponibilidad{
			{ID: "disp-1", TutorID: "11111111-1111-1111-1111-111111111111", Dia: "jueves", HoraIni: "15:00", HoraFin: "16:30"},
			{ID: "disp-2", TutorID: "11111111-1111-1111-1111-111111111111", Dia: "viernes", HoraIni: "10:00", HoraFin: "11:30"},
			{ID: "disp-3", TutorID: "22222222-2222-2222-2222-222222222222", Dia: "jueves", HoraIni: "17:00", HoraFin: "18:30"},
			{ID: "disp-4", TutorID: "33333333-3333-3333-3333-333333333333", Dia: "martes", HoraIni: "14:00", HoraFin: "15:30"},
		},
	}
}

func (m *MockBFFClient) BuscarTutores(_ context.Context, ramoCodigo string) ([]domain.TutorResumen, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var resultado []domain.TutorResumen
	for _, t := range m.tutores {
		for _, r := range t.Ramos {
			if strings.EqualFold(r, ramoCodigo) {
				resultado = append(resultado, t)
				break
			}
		}
	}
	return resultado, nil
}

func (m *MockBFFClient) VerDisponibilidad(_ context.Context, tutorID, dia string) ([]domain.BloqueDisponibilidad, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var resultado []domain.BloqueDisponibilidad
	for _, b := range m.disponibilidad {
		if b.TutorID != tutorID {
			continue
		}
		if dia != "" && !strings.EqualFold(b.Dia, dia) {
			continue
		}
		resultado = append(resultado, b)
	}
	return resultado, nil
}

func (m *MockBFFClient) CrearReserva(_ context.Context, tutorID, estudianteID, ramoCodigo, horario string) (domain.ReservaCreada, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.contadorID++
	reserva := domain.ReservaCreada{
		SesionID:     fmt.Sprintf("sesion-mock-%d", m.contadorID),
		TutorID:      tutorID,
		EstudianteID: estudianteID,
		RamoCodigo:   ramoCodigo,
		Estado:       "PENDIENTE",
		CreadaEn:     time.Now().UTC(),
	}
	m.reservas = append(m.reservas, reserva)
	return reserva, nil
}

func (m *MockBFFClient) GenerarQuiz(_ context.Context, _ string, ramoCodigo string) (domain.QuizResultado, error) {
	return domain.QuizResultado{
		RamoCodigo: ramoCodigo,
		Preguntas: []domain.PreguntaQuiz{
			{
				Pregunta:          fmt.Sprintf("¿Cuál es el concepto fundamental evaluado en la unidad 1 de %s?", ramoCodigo),
				Opciones:          []string{"A) Teorema Fundamental", "B) Integración por partes", "C) Series convergentes", "D) Límites laterales"},
				RespuestaCorrecta: "A) Teorema Fundamental",
			},
			{
				Pregunta:          fmt.Sprintf("Al resolver problemas de %s, ¿qué verificación se recomienda realizar primero?", ramoCodigo),
				Opciones:          []string{"A) Validar dominio y condiciones iniciales", "B) Ignorar restricciones", "C) Derivar sin simplificar", "D) Redondear al entero"},
				RespuestaCorrecta: "A) Validar dominio y condiciones iniciales",
			},
		},
	}, nil
}

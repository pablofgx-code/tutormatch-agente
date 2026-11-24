package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

const (
	defaultHTTPTimeout   = 10 * time.Second
	maxResponseBodyBytes = 2 << 20 // 2 MB de protección contra respuestas gigantes
	duracionBloque       = 90 * time.Minute
)

// NewPooledHTTPClient configura un pool reutilizable de conexiones HTTP/2 y TLS por host.
func NewPooledHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// zonaChile carga America/Santiago con fallback seguro para entornos Windows sin base tzdata.
func zonaChile() *time.Location {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return time.FixedZone("America/Santiago", -3*3600)
	}
	return loc
}

// HTTPBFFClient implementa domain.BFFClient sobre el endpoint GraphQL del BFF real.
type HTTPBFFClient struct {
	endpoint   string
	httpClient *http.Client
}

func NewHTTPBFFClient(endpoint string, httpClient *http.Client) *HTTPBFFClient {
	if httpClient == nil {
		httpClient = NewPooledHTTPClient(defaultHTTPTimeout)
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

// ejecutarGraphQL centraliza (DRY) las consultas y mutaciones hacia el BFF, reenviando el JWT.
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
	if authHeader := domain.AuthTokenDesdeContexto(ctx); authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

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

func (c *HTTPBFFClient) ListarRamos(ctx context.Context) ([]domain.Ramo, error) {
	const query = `
		query ListarRamos {
			ramos {
				codigo
				nombre
			}
		}`
	var out struct {
		Ramos []domain.Ramo `json:"ramos"`
	}
	err := c.ejecutarGraphQL(ctx, query, nil, &out)
	return out.Ramos, err
}

func (c *HTTPBFFClient) BuscarTutores(ctx context.Context, ramoCodigo string) ([]domain.TutorResumen, error) {
	const query = `
		query BuscarTutores($ramoCodigo: String!) {
			tutoresPorRamo(ramoCodigo: $ramoCodigo) {
				id
				nombre
				email
				promedioEvaluaciones
				cantidadEvaluaciones
				ramos {
					activo
					ramo {
						codigo
						nombre
					}
				}
			}
		}`
	var out struct {
		TutoresPorRamo []domain.TutorResumen `json:"tutoresPorRamo"`
	}
	err := c.ejecutarGraphQL(ctx, query, map[string]any{"ramoCodigo": ramoCodigo}, &out)
	return out.TutoresPorRamo, err
}

func (c *HTTPBFFClient) BuscarDisponibilidad(ctx context.Context, ramoCodigo string, desde, hasta time.Time) ([]domain.BloqueDisponibilidad, error) {
	const query = `
		query BuscarDisponibilidad($ramoCodigo: String!, $desde: DateTime!, $hasta: DateTime!) {
			buscarDisponibilidad(ramoCodigo: $ramoCodigo, desde: $desde, hasta: $hasta) {
				id
				tutorId
				ramoCodigo
				inicio
				fin
				libre
			}
		}`
	var out struct {
		BuscarDisponibilidad []domain.BloqueDisponibilidad `json:"buscarDisponibilidad"`
	}
	vars := map[string]any{
		"ramoCodigo": ramoCodigo,
		"desde":      desde.UTC().Format(time.RFC3339),
		"hasta":      hasta.UTC().Format(time.RFC3339),
	}
	err := c.ejecutarGraphQL(ctx, query, vars, &out)
	return out.BuscarDisponibilidad, err
}

func (c *HTTPBFFClient) VerDisponibilidad(ctx context.Context, tutorID string, desde, hasta time.Time) ([]domain.BloqueDisponibilidad, error) {
	const query = `
		query VerDisponibilidad($tutorId: ID!, $desde: DateTime!, $hasta: DateTime!) {
			disponibilidad(tutorId: $tutorId, desde: $desde, hasta: $hasta) {
				id
				tutorId
				ramoCodigo
				inicio
				fin
				libre
			}
		}`
	var out struct {
		Disponibilidad []domain.BloqueDisponibilidad `json:"disponibilidad"`
	}
	vars := map[string]any{
		"tutorId": tutorID,
		"desde":   desde.UTC().Format(time.RFC3339),
		"hasta":   hasta.UTC().Format(time.RFC3339),
	}
	err := c.ejecutarGraphQL(ctx, query, vars, &out)
	return out.Disponibilidad, err
}

// CrearReserva invoca la mutación crearSesion del BFF exigiendo el JWT en Authorization.
func (c *HTTPBFFClient) CrearReserva(ctx context.Context, tutorID, _ string, ramoCodigo string, inicio time.Time) (domain.ReservaCreada, error) {
	if domain.AuthTokenDesdeContexto(ctx) == "" {
		return domain.ReservaCreada{}, fmt.Errorf("%w: falta la cabecera Authorization para crear la reserva", domain.ErrServicioExterno)
	}

	const mutation = `
		mutation CrearSesion($input: CrearSesionInput!) {
			crearSesion(input: $input) {
				id
				tutorId
				estudianteId
				ramoCodigo
				inicio
				fin
				estado
				creadaEn
			}
		}`
	var out struct {
		CrearSesion domain.ReservaCreada `json:"crearSesion"`
	}
	vars := map[string]any{
		"input": map[string]any{
			"tutorId":    tutorID,
			"ramoCodigo": ramoCodigo,
			"inicio":     inicio.UTC().Format(time.RFC3339),
		},
	}
	err := c.ejecutarGraphQL(ctx, mutation, vars, &out)
	return out.CrearSesion, err
}

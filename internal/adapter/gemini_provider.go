package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

const (
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
	defaultGeminiModel   = "gemini-flash-lite-latest"
	defaultGeminiTimeout = 90 * time.Second
	defaultThinkingLevel = "LOW"
	defaultMaxRetries    = 2
	defaultRetryDelay    = 600 * time.Millisecond
)

var diasEspanol = map[time.Weekday]string{
	time.Sunday:    "domingo",
	time.Monday:    "lunes",
	time.Tuesday:   "martes",
	time.Wednesday: "miércoles",
	time.Thursday:  "jueves",
	time.Friday:    "viernes",
	time.Saturday:  "sábado",
}

func construirSystemPrompt(ahora time.Time) string {
	loc := zonaChile()
	local := ahora.In(loc)
	offset := local.Format("-07:00")

	return fmt.Sprintf(`Eres el asistente de tutorías universitarias de TutorMatch.
Responde siempre en español de forma clara, breve y amable.

Contexto temporal actual (zona horaria Chile - America/Santiago):
- Hoy es %s %s (hora local %s, desfase UTC %s).
- Todas las fechas enviadas a las herramientas deben ir en formato ISO-8601 UTC (sufijo Z). Para convertir hora de Chile a UTC, aplica el desfase %s.

Bloques oficiales de tutoría (lunes a viernes, 90 minutos cada uno, hora de Chile):
- Bloque A (mañana): 08:10 - 09:40
- Bloque B (mañana): 09:55 - 11:25
- Bloque C (mañana): 11:40 - 13:10
- Bloque D (tarde): 14:30 - 16:00
- Bloque E (tarde): 16:15 - 17:45
- Bloque F (tarde): 18:00 - 19:30

Reglas estrictas:
1. Usa siempre las herramientas disponibles; nunca inventes tutores, ramos ni horarios.
2. Si necesitas conocer el código oficial de un ramo, llama primero a listar_ramos.
3. Cuando un estudiante pida ayuda con un ramo en un día/franja (ej. "el jueves en la tarde"), calcula el rango UTC correspondiente (ej. bloques D, E y F de ese jueves) y llama en paralelo a buscar_disponibilidad y buscar_tutores.
4. Al mostrar horarios al estudiante, usa SIEMPRE el texto exacto del campo "horarioChile" devuelto por la herramienta (no recalcules zonas horarias manualmente).
5. Ejecuta crear_reserva ÚNICAMENTE cuando el estudiante haya confirmado el tutor y el horario exacto, enviando el timestamp exacto del campo "inicio" UTC devuelto por la disponibilidad.`,
		diasEspanol[local.Weekday()],
		local.Format("2006-01-02"),
		local.Format("15:04"),
		offset,
		offset,
	)
}

// GeminiProvider implementa domain.LLMProvider conectándose a Google Gemini en la nube.
type GeminiProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	maxRetries int
	retryDelay time.Duration
}

func NewGeminiProvider(apiKey, model string, httpClient *http.Client) *GeminiProvider {
	if strings.TrimSpace(model) == "" {
		model = defaultGeminiModel
	}
	if httpClient == nil {
		httpClient = NewPooledHTTPClient(defaultGeminiTimeout)
	}
	return &GeminiProvider{
		apiKey:     strings.TrimSpace(apiKey),
		model:      strings.TrimSpace(model),
		baseURL:    defaultGeminiBaseURL,
		httpClient: httpClient,
		maxRetries: defaultMaxRetries,
		retryDelay: defaultRetryDelay,
	}
}

// WithBaseURL permite redirigir peticiones a un servidor httptest en pruebas unitarias.
func (p *GeminiProvider) WithBaseURL(url string) *GeminiProvider {
	p.baseURL = strings.TrimRight(url, "/")
	return p
}

// --- Estructuras del protocolo REST de Gemini ---

type geminiRequest struct {
	SystemInstruction *geminiContent       `json:"systemInstruction,omitempty"`
	Contents          []geminiContent      `json:"contents"`
	Tools             []geminiToolSet      `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationCfg `json:"generationConfig,omitempty"`
}

type geminiGenerationCfg struct {
	ThinkingConfig *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolSet struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *GeminiProvider) GenerarTurno(ctx context.Context, mensajes []domain.MensajeChat, tools []domain.ToolDefinition) (domain.ModelTurn, error) {
	if p.apiKey == "" {
		return domain.ModelTurn{}, fmt.Errorf("GEMINI_API_KEY no configurada")
	}

	reqBody := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: construirSystemPrompt(time.Now())}},
		},
		Contents: mapearHistorial(mensajes),
		Tools:    mapearTools(tools),
		GenerationConfig: &geminiGenerationCfg{
			ThinkingConfig: &geminiThinkingConfig{
				ThinkingLevel: defaultThinkingLevel,
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return domain.ModelTurn{}, fmt.Errorf("error serializando petición a Gemini: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent", p.baseURL, p.model)

	var ultimoErr error
	for intento := 0; intento <= p.maxRetries; intento++ {
		turno, reintentable, err := p.ejecutarLlamada(ctx, url, payload)
		if err == nil {
			return turno, nil
		}
		ultimoErr = err
		if !reintentable || intento == p.maxRetries {
			break
		}

		espera := p.retryDelay * time.Duration(intento+1)
		log.Printf("[Gemini] Error transitorio (%v); reintentando en %v (intento %d/%d)...", err, espera, intento+1, p.maxRetries)
		if err := esperarConContexto(ctx, espera); err != nil {
			return domain.ModelTurn{}, err
		}
	}

	return domain.ModelTurn{}, ultimoErr
}

func (p *GeminiProvider) ejecutarLlamada(ctx context.Context, url string, payload []byte) (domain.ModelTurn, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return domain.ModelTurn{}, false, fmt.Errorf("error creando petición HTTP a Gemini: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return domain.ModelTurn{}, ctx.Err() == nil, fmt.Errorf("error llamando a Gemini: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return domain.ModelTurn{}, false, fmt.Errorf("error leyendo respuesta de Gemini: %w", err)
	}

	var gResp geminiResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		return domain.ModelTurn{}, esStatusReintentable(resp.StatusCode), fmt.Errorf("respuesta JSON inválida de Gemini (HTTP %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		reintentable := esStatusReintentable(resp.StatusCode)
		if gResp.Error != nil {
			return domain.ModelTurn{}, reintentable, fmt.Errorf("error API Gemini (%d): %s", gResp.Error.Code, gResp.Error.Message)
		}
		return domain.ModelTurn{}, reintentable, fmt.Errorf("error API Gemini HTTP %d", resp.StatusCode)
	}

	if len(gResp.Candidates) == 0 {
		return domain.ModelTurn{}, false, fmt.Errorf("Gemini no devolvió candidatos en la respuesta")
	}

	return parsearRespuestaGemini(gResp.Candidates[0].Content), false, nil
}

func esStatusReintentable(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusInternalServerError ||
		code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

func esperarConContexto(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// mapearHistorial convierte los mensajes agnósticos del dominio al formato de Gemini,
// agrupando respuestas de tools paralelas en un solo turno "user" como exige el protocolo.
func mapearHistorial(mensajes []domain.MensajeChat) []geminiContent {
	contents := make([]geminiContent, 0, len(mensajes))

	for _, msg := range mensajes {
		switch msg.Rol {
		case domain.RolUsuario:
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Texto}},
			})

		case domain.RolModelo:
			parts := make([]geminiPart, 0, len(msg.ToolCalls)+1)
			if msg.Texto != "" {
				parts = append(parts, geminiPart{Text: msg.Texto})
			}
			for _, tc := range msg.ToolCalls {
				parts = append(parts, geminiPart{
					ThoughtSignature: tc.Signature,
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: tc.Arguments,
					},
				})
			}
			contents = append(contents, geminiContent{
				Role:  "model",
				Parts: parts,
			})

		case domain.RolHerramienta:
			if msg.ToolResult == nil {
				continue
			}
			part := geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name: msg.ToolResult.Name,
					Response: map[string]any{
						"resultado": msg.ToolResult.Content,
						"es_error":  msg.ToolResult.IsError,
					},
				},
			}

			if n := len(contents); n > 0 && contents[n-1].Role == "user" && len(contents[n-1].Parts) > 0 && contents[n-1].Parts[0].FunctionResponse != nil {
				contents[n-1].Parts = append(contents[n-1].Parts, part)
			} else {
				contents = append(contents, geminiContent{
					Role:  "user",
					Parts: []geminiPart{part},
				})
			}
		}
	}

	return contents
}

func mapearTools(tools []domain.ToolDefinition) []geminiToolSet {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]geminiFunctionDecl, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, geminiFunctionDecl{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return []geminiToolSet{{FunctionDeclarations: decls}}
}

func parsearRespuestaGemini(content geminiContent) domain.ModelTurn {
	var turno domain.ModelTurn
	var textos []string

	for i, part := range content.Parts {
		if part.Text != "" && !part.Thought {
			textos = append(textos, part.Text)
		}
		if part.FunctionCall != nil {
			args := part.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			turno.ToolCalls = append(turno.ToolCalls, domain.ToolCall{
				ID:        fmt.Sprintf("call_%d_%s", i, part.FunctionCall.Name),
				Name:      part.FunctionCall.Name,
				Arguments: args,
				Signature: part.ThoughtSignature,
			})
		}
	}

	turno.Text = strings.TrimSpace(strings.Join(textos, "\n"))
	return turno
}

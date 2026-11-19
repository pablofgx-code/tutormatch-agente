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
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
	defaultGeminiModel   = "gemini-flash-lite-latest"
	defaultGeminiTimeout = 90 * time.Second
	defaultThinkingLevel = "LOW"
)

const systemPromptTutorMatch = `Eres el asistente de tutorías universitarias de TutorMatch.
Responde siempre en español de forma clara, breve y amable.
Catálogo de códigos de ramos conocidos:
- MAT1610: Cálculo I
- MAT1620: Cálculo II
- IIC1103: Introducción a la Programación
- IIC2133: Estructuras de Datos y Algoritmos
- FIS1514: Dinámica

Reglas estrictas:
1. Usa siempre las herramientas disponibles para consultar tutores, horarios o generar quizzes; nunca inventes tutores ni horarios.
2. Cuando un estudiante pida ayuda con un ramo y un día/horario, primero busca tutores con buscar_tutores y luego consulta la disponibilidad de TODOS los tutores encontrados usando ver_disponibilidad.
3. Propón todas las opciones disponibles al estudiante y ejecuta crear_reserva ÚNICAMENTE cuando el estudiante haya confirmado el tutor y el horario.`

// GeminiProvider implementa domain.LLMProvider conectándose a Google Gemini en la nube.
type GeminiProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

func NewGeminiProvider(apiKey, model string, httpClient *http.Client) *GeminiProvider {
	if strings.TrimSpace(model) == "" {
		model = defaultGeminiModel
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultGeminiTimeout}
	}
	return &GeminiProvider{
		apiKey:     strings.TrimSpace(apiKey),
		model:      strings.TrimSpace(model),
		baseURL:    defaultGeminiBaseURL,
		httpClient: httpClient,
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
			Parts: []geminiPart{{Text: systemPromptTutorMatch}},
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return domain.ModelTurn{}, fmt.Errorf("error creando petición HTTP a Gemini: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return domain.ModelTurn{}, fmt.Errorf("error llamando a Gemini: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return domain.ModelTurn{}, fmt.Errorf("error leyendo respuesta de Gemini: %w", err)
	}

	var gResp geminiResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		return domain.ModelTurn{}, fmt.Errorf("respuesta JSON inválida de Gemini (HTTP %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if gResp.Error != nil {
			return domain.ModelTurn{}, fmt.Errorf("error API Gemini (%d): %s", gResp.Error.Code, gResp.Error.Message)
		}
		return domain.ModelTurn{}, fmt.Errorf("error API Gemini HTTP %d", resp.StatusCode)
	}

	if len(gResp.Candidates) == 0 {
		return domain.ModelTurn{}, fmt.Errorf("Gemini no devolvió candidatos en la respuesta")
	}

	return parsearRespuestaGemini(gResp.Candidates[0].Content), nil
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
					// Gemini exige que "response" sea siempre un objeto JSON.
					Response: map[string]any{
						"resultado": msg.ToolResult.Content,
						"es_error":  msg.ToolResult.IsError,
					},
				},
			}

			// Si el último turno ya contiene functionResponses paralelas, las agrupamos juntas.
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

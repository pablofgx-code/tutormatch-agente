# CLAUDE.md - Contexto del Proyecto `tutormatch-agente`

## Visión general
Microservicio de **Agente de IA y Servidor MCP** de la plataforma **TutorMatch**, desarrollado en **Go** (sin dependencias externas, solo biblioteca estándar)[cite: 1]. Expone el endpoint REST `POST /agent/chat` consumido por el BFF y un servidor **MCP (JSON-RPC 2.0)** en `POST /mcp`[cite: 1].

## Arquitectura y estructura de paquetes
Aplica Arquitectura Limpia (Clean Architecture) y principios SOLID (SRP, OCP, ISP, DIP):
* `cmd/server/main.go`: Punto de entrada, carga de variables de entorno, selección dinámica entre `HTTPBFFClient` y `MockBFFClient`, inyección de dependencias y *Graceful Shutdown*[cite: 1].
* `internal/domain/`: Entidades agnósticas al proveedor de IA (`ChatRequest`, `MensajeChat`, `ToolDefinition`, `ModelTurn`), errores de dominio y puertos segregados (`LLMProvider`, `TutorClient`, `SesionesClient`, `MaterialClient`, `BFFClient`, `Tool`, `ToolRegistry`)[cite: 1].
* `internal/mcp/tools.go`: Patrón **Strategy/Command + Registry** ($O(1)$ con orden determinista) que implementa las 4 herramientas oficiales: `buscar_tutores`, `ver_disponibilidad`, `crear_reserva` y `generar_quiz`[cite: 1].
* `internal/service/agent_service.go`: Orquestador del loop del agente con límite de iteraciones (`maxIteraciones = 6`), *fast-path* secuencial para 1 tool, concurrencia **Fan-Out / Fan-In** sin locks para múltiples tools y manejo *fail-soft* de errores[cite: 1].
* `internal/adapter/gemini_provider.go`: Cliente REST nativo hacia Google Gemini (`x-goog-api-key`, `thinkingLevel: "LOW"`, preservación de `thoughtSignature` y agrupación de `functionResponse` paralelas)[cite: 1].
* `internal/adapter/bff_client.go`: Cliente GraphQL (`HTTPBFFClient`) y mock en memoria seguro para concurrencia con `sync.RWMutex` (`MockBFFClient`)[cite: 1].
* `internal/http/handler.go`: Endpoints `GET /health`, `POST /agent/chat` y `POST /mcp` (`initialize`, `tools/list`, `tools/call`) con `Content-Type: application/json; charset=utf-8` y límite de cuerpo de 1 MB[cite: 1].

## Reglas e invariantes críticas
1. **Protección contra Prompt Injection**: En `crear_reserva`, el `estudiante_id` proviene siempre del `usuario_id` validado por el BFF (o el header `X-Usuario-Id` en `/mcp`), nunca de los argumentos generados por el LLM[cite: 1].
2. **Seguridad de credenciales**: `GEMINI_API_KEY` se inyecta exclusivamente por variable de entorno o Secret de Kubernetes y se envía por el header `x-goog-api-key`, jamás por query string ni versionada en Git[cite: 1].
3. **Resiliencia del loop**: Errores de parámetros o herramientas desconocidas se devuelven al modelo con `IsError: true` para autocorrección; bucles infinitos se cortan con `domain.ErrMaxIteraciones` (HTTP 504)[cite: 1].
4. **Modo desacoplado**: Si `BFF_URL` está vacía, el servicio arranca automáticamente con `MockBFFClient`[cite: 1].

## Comandos principales
* **Ejecutar servidor local:** `go run ./cmd/server/main.go`
* **Análisis estático:** `go vet ./...`
* **Tests y cobertura (mínimo exigido 60%):** `go test -v -cover ./internal/...`
* **Levantar en Docker:** `docker compose up -d --build`
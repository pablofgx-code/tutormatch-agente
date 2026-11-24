# CLAUDE.md - Contexto del Proyecto `tutormatch-agente`

## Visión general
Microservicio de **Agente de IA y Servidor MCP** de la plataforma **TutorMatch**, desarrollado en **Go** (solo biblioteca estándar). Expone `POST /agent/chat` para el BFF y `POST /mcp` (JSON-RPC 2.0) en el puerto `8081`.

## Arquitectura y patrones aplicados
Aplica Clean Architecture, SOLID (SRP, OCP, ISP, DIP) y DRY:
* `cmd/server/main.go`: Carga `.env` en desarrollo (sin sobrescribir variables del entorno), inyecta dependencias y configura *Graceful Shutdown*.
* `internal/domain/`: Entidades, validación de largo de entrada (máx. 2000 caracteres), contexto de autenticación (`ConAuthToken`) y puertos segregados (`LLMProvider`, `TutorClient`, `SesionesClient`, `BFFClient`, `ConversationStore`, `Tool`, `ToolRegistry`).
* `internal/mcp/tools.go`: Patrón **Strategy/Command + Registry ($O(1)$)** para las 5 tools (`listar_ramos`, `buscar_tutores`, `buscar_disponibilidad`, `ver_disponibilidad`, `crear_reserva`) con conversión determinista de bloques UTC a hora Chile (`horarioChile`).
* `internal/service/agent_service.go`: Orquestador del loop conversacional con tope de iteraciones, concurrencia **Fan-Out / Fan-In** sin locks, modo *fail-soft* y reporte de `acciones` (`sesion.creada`).
* `internal/adapter/bff_client.go`: Adaptador GraphQL alineado a `BFF/schema.gql`, con *Connection Pooling* HTTP y reenvío de `Authorization: Bearer <token>`.
* `internal/adapter/cached_bff_client.go`: Patrón **Decorator** con caché TTL de 10 minutos para `ListarRamos`.
* `internal/adapter/mock_bff_client.go`: Mock en memoria seguro para concurrencia con bloques reales de 90 minutos en UTC.
* `internal/adapter/memory_store.go`: Historial acotado **LRU + TTL en $O(1)$** (`container/list` + `map`) con recorte seguro sobre turnos `RolUsuario`.
* `internal/adapter/gemini_provider.go`: Cliente REST nativo hacia Gemini (`x-goog-api-key`, `thinkingLevel: "LOW"`, `thoughtSignature`, reintentos con backoff ante `429`/`503` y prompt con fecha/bloques de Chile).
* `internal/http/handler.go` y `internal/http/ratelimit.go`: Enrutamiento HTTP, cabeceras defensivas, saneamiento de errores internos y **Token Bucket Rate Limiter** en $O(1)$ acotado por LRU.

## Reglas e invariantes críticas
1. **Autenticación delegada al BFF**: El BFF reenvía `Authorization: Bearer <token>` a `POST /agent/chat`, y `HTTPBFFClient` lo propaga en `crearSesion` para que el BFF identifique al estudiante real.
2. **Aislamiento de `/mcp`**: En Kubernetes el Service `agente` es `ClusterIP` interno y nunca se expone fuera del clúster.
3. **Resiliencia y memoria acotada**: Tanto el historial de conversaciones como el rate limiter usan expulsión LRU en $O(1)$ para evitar OOM dentro del límite de 256Mi.

## Comandos principales
* **Servidor local:** `go run ./cmd/server/main.go`
* **Análisis estático:** `go vet ./...`
* **Tests y cobertura (mínimo 60%):** `go test -v -cover ./internal/...`
* **Docker:** `docker compose up -d --build`
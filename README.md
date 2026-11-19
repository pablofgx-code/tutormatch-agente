# tutormatch-agente

Microservicio de **Agente de IA y Servidor MCP** para la plataforma **TutorMatch**, implementado en Go con Arquitectura Limpia y cero dependencias externas[cite: 1].

## Endpoints

| Método | Ruta | Descripción |
| :--- | :--- | :--- |
| `GET` | `/health` | Verificación de estado (`{"status":"ok"}`)[cite: 1]. |
| `POST` | `/agent/chat` | Ejecuta el loop conversacional con Gemini y las herramientas MCP[cite: 1]. |
| `POST` | `/mcp` | Servidor MCP (JSON-RPC 2.0): soporta `initialize`, `tools/list` y `tools/call`[cite: 1]. |

### Herramientas MCP disponibles
* `buscar_tutores`: Busca tutores activos por código de ramo (ej. `MAT1620`)[cite: 1].
* `ver_disponibilidad`: Consulta horarios libres de un tutor, filtrable por día[cite: 1].
* `crear_reserva`: Registra una reserva en estado `PENDIENTE` usando el `usuario_id` autenticado[cite: 1].
* `generar_quiz`: Genera preguntas de práctica a partir del ramo o una sesión[cite: 1].

## Variables de entorno

| Variable | Requerida | Por defecto | Descripción |
| :--- | :---: | :--- | :--- |
| `GEMINI_API_KEY` | Sí | — | API Key gratuita de Google AI Studio[cite: 1]. |
| `GEMINI_MODEL` | No | `gemini-flash-lite-latest` | Modelo de Gemini a utilizar. |
| `PORT` | No | `8080` | Puerto HTTP del servidor[cite: 1]. |
| `BFF_URL` | No | *(vacío = Mock)* | Endpoint GraphQL del BFF; si se omite usa `MockBFFClient` en memoria[cite: 1]. |

## Ejecución local

### PowerShell (Windows)
```powershell
$env:GEMINI_API_KEY="TU_CLAVE_AQUI"
go run ./cmd/server/main.go
```

### Bash (Linux / macOS / WSL)
```bash
export GEMINI_API_KEY="TU_CLAVE_AQUI"
go run ./cmd/server/main.go
```

## Pruebas y cobertura

```powershell
go vet ./...
go test -v "-coverprofile=coverage.out" ./internal/...
go tool cover "-func=coverage.out"
Remove-Item coverage.out
```

## Ejecución con Docker

```powershell
$env:GEMINI_API_KEY="TU_CLAVE_AQUI"
docker compose up -d --build
```

## Despliegue en Kubernetes

Primero crea el Secret con tu clave (nunca la incluyas en los manifiestos):

```bash
kubectl create secret generic tutormatch-agente-secrets \
  --from-literal=gemini-api-key="TU_CLAVE_AQUI"
kubectl apply -f k8s/deployment.yaml
```
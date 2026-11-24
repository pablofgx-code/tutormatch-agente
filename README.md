# tutormatch-agente

Microservicio de **Agente de IA y Servidor MCP** para la plataforma **TutorMatch**, implementado en Go con Arquitectura Limpia y cero dependencias externas.

## Endpoints

| Método | Ruta | Descripción |
| :--- | :--- | :--- |
| `GET` | `/health` | Verificación de estado (`{"status":"ok"}`). |
| `POST` | `/agent/chat` | Ejecuta el loop conversacional con Gemini y las herramientas MCP. |
| `POST` | `/mcp` | Servidor MCP (JSON-RPC 2.0): `initialize`, `notifications/initialized`, `ping`, `tools/list` y `tools/call`. |

### Herramientas MCP disponibles
* `listar_ramos`: Consulta el catálogo oficial de ramos (`ramos`).
* `buscar_tutores`: Busca tutores activos por código de ramo con su promedio de evaluaciones (`tutoresPorRamo`).
* `buscar_disponibilidad`: Consulta casillas libres de 90 minutos de todos los tutores de un ramo en un rango UTC (`buscarDisponibilidad`).
* `ver_disponibilidad`: Consulta las casillas de un tutor específico en un rango UTC (`disponibilidad`).
* `crear_reserva`: Reserva una casilla libre en estado `PENDIENTE` (`crearSesion`), reenviando el JWT recibido en `Authorization`.

## Variables de entorno

| Variable | Requerida | Por defecto | Descripción |
| :--- | :---: | :--- | :--- |
| `GEMINI_API_KEY` | Sí | — | API Key de Google AI Studio. |
| `GEMINI_MODEL` | No | `gemini-flash-lite-latest` | Modelo de Gemini a utilizar. |
| `PORT` | No | `8081` | Puerto HTTP del servidor (evita choque con Sesiones en `8080`). |
| `BFF_URL` | No | *(vacío = Mock)* | Endpoint GraphQL del BFF (ej. `http://localhost:3000/graphql`); si se omite usa `MockBFFClient` en memoria. |

## Ejecución local

Crea un archivo `.env` a partir de `.env.example` con tu `GEMINI_API_KEY` (el servidor lo carga automáticamente en desarrollo sin pisar variables del entorno) y ejecuta:

```powershell
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
docker compose up -d --build
```

## Despliegue en Kubernetes

Crea primero el Secret con tu clave y luego aplica el manifiesto:

```powershell
kubectl create secret generic agente-secrets --from-literal=gemini-api-key="TU_CLAVE_AQUI"
kubectl apply -f k8s/deployment.yaml
```
> **Restricciones de despliegue:** El Deployment debe mantenerse con **`replicas: 1`** porque el historial de conversaciones vive en memoria (se pierde al reiniciar el pod; escalar a múltiples réplicas requiere un store compartido como Redis). El Service es **`ClusterIP` interno** y nunca debe exponerse fuera del clúster, ya que `/mcp` lee `X-Usuario-Id` directamente.
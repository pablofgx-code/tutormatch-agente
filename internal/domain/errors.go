package domain

import "errors"

var (
	ErrArgumentoInvalido = errors.New("datos de entrada inválidos")
	ErrToolDesconocida   = errors.New("herramienta MCP no reconocida")
	ErrProveedorIA       = errors.New("error al comunicarse con el modelo de IA")
	ErrServicioExterno   = errors.New("error al consultar el BFF o microservicio")
	ErrMaxIteraciones    = errors.New("el agente superó el máximo de pasos permitidos")
)

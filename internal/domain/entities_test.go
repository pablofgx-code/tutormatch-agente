package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestChatRequest_ValidarYContextoAuth(t *testing.T) {
	casos := []struct {
		nombre string
		req    ChatRequest
		errEsp error
	}{
		{"valido", ChatRequest{UsuarioID: "u-1", ConversacionID: "c-1", Mensaje: "Hola"}, nil},
		{"usuario vacio", ChatRequest{UsuarioID: "   ", Mensaje: "Hola"}, ErrArgumentoInvalido},
		{"mensaje vacio", ChatRequest{UsuarioID: "u-1", Mensaje: "   "}, ErrArgumentoInvalido},
		{"mensaje excede limite", ChatRequest{UsuarioID: "u-1", Mensaje: strings.Repeat("a", 2001)}, ErrArgumentoInvalido},
		{"conversacion excede limite", ChatRequest{UsuarioID: "u-1", ConversacionID: strings.Repeat("x", 129), Mensaje: "Hola"}, ErrArgumentoInvalido},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			if err := tc.req.Validar(); !errors.Is(err, tc.errEsp) {
				t.Fatalf("esperaba %v, obtuve %v", tc.errEsp, err)
			}
		})
	}

	ctx := ConAuthToken(context.Background(), "  Bearer tok-123  ")
	if got := AuthTokenDesdeContexto(ctx); got != "Bearer tok-123" {
		t.Fatalf("token inesperado en contexto: %q", got)
	}
	if got := AuthTokenDesdeContexto(context.Background()); got != "" {
		t.Fatalf("se esperaba vacío sin token, obtuve %q", got)
	}
}

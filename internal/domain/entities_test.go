package domain

import (
	"errors"
	"testing"
)

func TestChatRequest_Validar(t *testing.T) {
	casos := []struct {
		nombre string
		req    ChatRequest
		errEsp error
	}{
		{
			nombre: "valido",
			req:    ChatRequest{UsuarioID: "u-1", Mensaje: "Hola"},
			errEsp: nil,
		},
		{
			nombre: "usuario vacio",
			req:    ChatRequest{UsuarioID: "   ", Mensaje: "Hola"},
			errEsp: ErrArgumentoInvalido,
		},
		{
			nombre: "mensaje vacio",
			req:    ChatRequest{UsuarioID: "u-1", Mensaje: "   "},
			errEsp: ErrArgumentoInvalido,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			err := tc.req.Validar()
			if !errors.Is(err, tc.errEsp) {
				t.Fatalf("esperaba %v, obtuve %v", tc.errEsp, err)
			}
		})
	}
}

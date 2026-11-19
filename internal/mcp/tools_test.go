package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/tutormatch/agente/internal/adapter"
	"github.com/tutormatch/agente/internal/domain"
)

func TestRegistry_OrdenDeterministaYReemplazo(t *testing.T) {
	bff := adapter.NewMockBFFClient()
	reg := NewDefaultRegistry(bff)

	defs := reg.Definitions()
	esperado := []string{"buscar_tutores", "ver_disponibilidad", "crear_reserva", "generar_quiz"}
	if len(defs) != len(esperado) {
		t.Fatalf("esperaba %d tools, obtuve %d", len(esperado), len(defs))
	}
	for i, nombre := range esperado {
		if defs[i].Name != nombre {
			t.Fatalf("posición %d: esperaba %q, obtuve %q", i, nombre, defs[i].Name)
		}
	}

	// Re-registrar una tool existente no debe duplicar su entrada en Definitions()
	reg.Register(&BuscarTutoresTool{client: bff})
	if len(reg.Definitions()) != len(esperado) {
		t.Fatalf("re-registrar duplicó definiciones: %d", len(reg.Definitions()))
	}

	if _, ok := reg.Get("tool_desconocida"); ok {
		t.Fatal("se esperaba ok=false para una tool inexistente")
	}
}

func TestTools_EjecucionExitosa(t *testing.T) {
	reg := NewDefaultRegistry(adapter.NewMockBFFClient())
	ctx := context.Background()

	// 1. buscar_tutores (normaliza minúsculas a mayúsculas)
	toolBuscar, _ := reg.Get("buscar_tutores")
	outBuscar, err := toolBuscar.Execute(ctx, "est-1", map[string]any{"ramo_codigo": "mat1620"})
	if err != nil {
		t.Fatalf("buscar_tutores falló: %v", err)
	}
	tutores, ok := outBuscar.([]domain.TutorResumen)
	if !ok || len(tutores) != 2 {
		t.Fatalf("se esperaban 2 tutores para MAT1620, obtuve %+v", outBuscar)
	}

	// 2. ver_disponibilidad (normaliza día a minúsculas)
	toolDisp, _ := reg.Get("ver_disponibilidad")
	outDisp, err := toolDisp.Execute(ctx, "est-1", map[string]any{
		"tutor_id": "11111111-1111-1111-1111-111111111111",
		"dia":      "JUEVES",
	})
	if err != nil {
		t.Fatalf("ver_disponibilidad falló: %v", err)
	}
	bloques, ok := outDisp.([]domain.BloqueDisponibilidad)
	if !ok || len(bloques) != 1 {
		t.Fatalf("se esperaba 1 bloque el jueves, obtuve %+v", outDisp)
	}

	// 3. crear_reserva (usa usuarioID autenticado)
	toolRes, _ := reg.Get("crear_reserva")
	outRes, err := toolRes.Execute(ctx, "est-autenticado", map[string]any{
		"tutor_id":    "11111111-1111-1111-1111-111111111111",
		"ramo_codigo": "mat1620",
		"horario":     "2026-11-26T15:00:00Z",
	})
	if err != nil {
		t.Fatalf("crear_reserva falló: %v", err)
	}
	reserva, ok := outRes.(domain.ReservaCreada)
	if !ok || reserva.EstudianteID != "est-autenticado" || reserva.RamoCodigo != "MAT1620" {
		t.Fatalf("reserva inesperada: %+v", outRes)
	}

	// 4. generar_quiz (sesion_id opcional)
	toolQuiz, _ := reg.Get("generar_quiz")
	outQuiz, err := toolQuiz.Execute(ctx, "est-1", map[string]any{"ramo_codigo": "mat1620"})
	if err != nil {
		t.Fatalf("generar_quiz falló: %v", err)
	}
	quiz, ok := outQuiz.(domain.QuizResultado)
	if !ok || quiz.RamoCodigo != "MAT1620" || len(quiz.Preguntas) == 0 {
		t.Fatalf("quiz inesperado: %+v", outQuiz)
	}
}

func TestTools_ValidacionArgumentos(t *testing.T) {
	reg := NewDefaultRegistry(adapter.NewMockBFFClient())
	ctx := context.Background()

	casos := []struct {
		nombre    string
		tool      string
		usuarioID string
		args      map[string]any
	}{
		{"buscar_tutores sin ramo", "buscar_tutores", "u1", map[string]any{}},
		{"buscar_tutores tipo invalido", "buscar_tutores", "u1", map[string]any{"ramo_codigo": 123}},
		{"buscar_tutores vacio", "buscar_tutores", "u1", map[string]any{"ramo_codigo": "   "}},
		{"ver_disponibilidad sin tutor", "ver_disponibilidad", "u1", map[string]any{}},
		{"ver_disponibilidad dia invalido", "ver_disponibilidad", "u1", map[string]any{"tutor_id": "t1", "dia": 42}},
		{"crear_reserva sin tutor", "crear_reserva", "u1", map[string]any{"ramo_codigo": "MAT1620", "horario": "2026-11-26T15:00:00Z"}},
		{"crear_reserva sin ramo", "crear_reserva", "u1", map[string]any{"tutor_id": "t1", "horario": "2026-11-26T15:00:00Z"}},
		{"crear_reserva sin horario", "crear_reserva", "u1", map[string]any{"tutor_id": "t1", "ramo_codigo": "MAT1620"}},
		{"generar_quiz sin ramo", "generar_quiz", "u1", map[string]any{}},
		{"generar_quiz sesion invalida", "generar_quiz", "u1", map[string]any{"ramo_codigo": "MAT1620", "sesion_id": true}},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			tool, _ := reg.Get(tc.tool)
			_, err := tool.Execute(ctx, tc.usuarioID, tc.args)
			if !errors.Is(err, domain.ErrArgumentoInvalido) {
				t.Fatalf("esperaba ErrArgumentoInvalido, obtuve %v", err)
			}
		})
	}
}

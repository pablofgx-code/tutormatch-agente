package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tutormatch/agente/internal/adapter"
	"github.com/tutormatch/agente/internal/domain"
)

func TestRegistry_OrdenDeterministaYReemplazo(t *testing.T) {
	bff := adapter.NewMockBFFClient()
	reg := NewDefaultRegistry(bff)

	defs := reg.Definitions()
	esperado := []string{"listar_ramos", "buscar_tutores", "buscar_disponibilidad", "ver_disponibilidad", "crear_reserva"}
	if len(defs) != len(esperado) {
		t.Fatalf("esperaba %d tools, obtuve %d", len(esperado), len(defs))
	}
	for i, nombre := range esperado {
		if defs[i].Name != nombre {
			t.Fatalf("posición %d: esperaba %q, obtuve %q", i, nombre, defs[i].Name)
		}
	}

	reg.Register(&BuscarTutoresTool{client: bff})
	if len(reg.Definitions()) != len(esperado) {
		t.Fatalf("re-registrar duplicó definiciones: %d", len(reg.Definitions()))
	}
	if _, ok := reg.Get("tool_desconocida"); ok {
		t.Fatal("se esperaba ok=false para tool inexistente")
	}
}

func TestTools_EjecucionExitosa(t *testing.T) {
	bff := adapter.NewMockBFFClient()
	reg := NewDefaultRegistry(bff)
	ctx := context.Background()

	desde := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	hasta := time.Now().UTC().AddDate(0, 0, 14).Format(time.RFC3339)

	// 1. listar_ramos
	toolRamos, _ := reg.Get("listar_ramos")
	outRamos, err := toolRamos.Execute(ctx, "est-1", nil)
	if err != nil {
		t.Fatalf("listar_ramos falló: %v", err)
	}
	if ramos, ok := outRamos.([]domain.Ramo); !ok || len(ramos) < 3 {
		t.Fatalf("ramos inesperados: %+v", outRamos)
	}

	// 2. buscar_tutores
	toolBuscar, _ := reg.Get("buscar_tutores")
	outBuscar, err := toolBuscar.Execute(ctx, "est-1", map[string]any{"ramo_codigo": "mat1620"})
	if err != nil {
		t.Fatalf("buscar_tutores falló: %v", err)
	}
	if tutores, ok := outBuscar.([]domain.TutorResumen); !ok || len(tutores) != 2 {
		t.Fatalf("se esperaban 2 tutores para MAT1620, obtuve %+v", outBuscar)
	}

	// 3. buscar_disponibilidad (verifica enriquecimiento con horarioChile)
	toolBuscDisp, _ := reg.Get("buscar_disponibilidad")
	outBuscDisp, err := toolBuscDisp.Execute(ctx, "est-1", map[string]any{
		"ramo_codigo": "mat1620",
		"desde":       desde,
		"hasta":       hasta,
	})
	if err != nil {
		t.Fatalf("buscar_disponibilidad falló: %v", err)
	}
	bloques, ok := outBuscDisp.([]bloqueEnriquecido)
	if !ok || len(bloques) == 0 || !strings.Contains(bloques[0].HorarioChile, "Bloque") {
		t.Fatalf("bloques enriquecidos inesperados: %+v", outBuscDisp)
	}

	// 4. ver_disponibilidad
	toolVerDisp, _ := reg.Get("ver_disponibilidad")
	outVerDisp, err := toolVerDisp.Execute(ctx, "est-1", map[string]any{
		"tutor_id": "11111111-1111-1111-1111-111111111111",
		"desde":    desde,
		"hasta":    hasta,
	})
	if err != nil {
		t.Fatalf("ver_disponibilidad falló: %v", err)
	}
	if vBloques, ok := outVerDisp.([]bloqueEnriquecido); !ok || len(vBloques) != 2 {
		t.Fatalf("se esperaban 2 bloques para Camila, obtuve %+v", outVerDisp)
	}

	// 5. crear_reserva usando una casilla real libre del mock
	toolRes, _ := reg.Get("crear_reserva")
	outRes, err := toolRes.Execute(ctx, "est-autenticado", map[string]any{
		"tutor_id":    bloques[0].TutorID,
		"ramo_codigo": "mat1620",
		"inicio":      bloques[0].Inicio.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("crear_reserva falló: %v", err)
	}
	reserva, ok := outRes.(domain.ReservaCreada)
	if !ok || reserva.EstudianteID != "est-autenticado" || reserva.RamoCodigo != "MAT1620" {
		t.Fatalf("reserva inesperada: %+v", outRes)
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
		{"buscar_disponibilidad sin ramo", "buscar_disponibilidad", "u1", map[string]any{}},
		{"buscar_disponibilidad fecha invalida", "buscar_disponibilidad", "u1", map[string]any{"ramo_codigo": "MAT1620", "desde": "jueves", "hasta": "2026-11-27T00:00:00Z"}},
		{"buscar_disponibilidad rango invertido", "buscar_disponibilidad", "u1", map[string]any{"ramo_codigo": "MAT1620", "desde": "2026-11-27T00:00:00Z", "hasta": "2026-11-26T00:00:00Z"}},
		{"ver_disponibilidad sin tutor", "ver_disponibilidad", "u1", map[string]any{}},
		{"ver_disponibilidad sin hasta", "ver_disponibilidad", "u1", map[string]any{"tutor_id": "t1", "desde": "2026-11-26T00:00:00Z"}},
		{"ver_disponibilidad rango invertido", "ver_disponibilidad", "u1", map[string]any{"tutor_id": "t1", "desde": "2026-11-27T00:00:00Z", "hasta": "2026-11-26T00:00:00Z"}},
		{"crear_reserva sin auth", "crear_reserva", "", map[string]any{"tutor_id": "t1", "ramo_codigo": "MAT1620", "inicio": "2026-11-26T17:30:00Z"}},
		{"crear_reserva sin tutor", "crear_reserva", "u1", map[string]any{"ramo_codigo": "MAT1620", "inicio": "2026-11-26T17:30:00Z"}},
		{"crear_reserva sin ramo", "crear_reserva", "u1", map[string]any{"tutor_id": "t1", "inicio": "2026-11-26T17:30:00Z"}},
		{"crear_reserva inicio invalido", "crear_reserva", "u1", map[string]any{"tutor_id": "t1", "ramo_codigo": "MAT1620", "inicio": "mañana"}},
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

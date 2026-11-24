package adapter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

// MockBFFClient simula el BFF en memoria con bloques reales de 90 min en UTC para desarrollo local.
type MockBFFClient struct {
	mu             sync.RWMutex
	ramos          []domain.Ramo
	tutores        []domain.TutorResumen
	disponibilidad []domain.BloqueDisponibilidad
	reservas       []domain.ReservaCreada
	contadorID     int
}

func NewMockBFFClient() *MockBFFClient {
	ramos := []domain.Ramo{
		{Codigo: "MAT1610", Nombre: "Cálculo I"},
		{Codigo: "MAT1620", Nombre: "Cálculo II"},
		{Codigo: "IIC1103", Nombre: "Introducción a la Programación"},
		{Codigo: "IIC2133", Nombre: "Estructuras de Datos y Algoritmos"},
		{Codigo: "FIS1514", Nombre: "Dinámica"},
	}

	juevesBloqueD := proximoBloqueChile(time.Thursday, 14, 30)
	juevesBloqueE := proximoBloqueChile(time.Thursday, 16, 15)
	viernesBloqueB := proximoBloqueChile(time.Friday, 9, 55)
	martesBloqueD := proximoBloqueChile(time.Tuesday, 14, 30)

	return &MockBFFClient{
		ramos: ramos,
		tutores: []domain.TutorResumen{
			{
				ID:                   "11111111-1111-1111-1111-111111111111",
				Nombre:               "Camila Rojas",
				Email:                "camila.rojas@ucn.cl",
				PromedioEvaluaciones: 4.8,
				CantidadEvaluaciones: 12,
				Ramos: []domain.RamoTutor{
					{Ramo: ramos[0], Activo: true},
					{Ramo: ramos[1], Activo: true},
				},
			},
			{
				ID:                   "22222222-2222-2222-2222-222222222222",
				Nombre:               "Matías Vega",
				Email:                "matias.vega@ucn.cl",
				PromedioEvaluaciones: 4.6,
				CantidadEvaluaciones: 7,
				Ramos: []domain.RamoTutor{
					{Ramo: ramos[1], Activo: true},
					{Ramo: ramos[2], Activo: true},
					{Ramo: ramos[3], Activo: true},
				},
			},
			{
				ID:                   "33333333-3333-3333-3333-333333333333",
				Nombre:               "Sofía Morales",
				Email:                "sofia.morales@ucn.cl",
				PromedioEvaluaciones: 5.0,
				CantidadEvaluaciones: 4,
				Ramos: []domain.RamoTutor{
					{Ramo: ramos[4], Activo: true},
					{Ramo: ramos[0], Activo: true},
				},
			},
		},
		disponibilidad: []domain.BloqueDisponibilidad{
			{
				ID:         "disp-1",
				TutorID:    "11111111-1111-1111-1111-111111111111",
				RamoCodigo: "MAT1620",
				Inicio:     juevesBloqueD,
				Fin:        juevesBloqueD.Add(duracionBloque),
				Libre:      true,
			},
			{
				ID:         "disp-2",
				TutorID:    "11111111-1111-1111-1111-111111111111",
				RamoCodigo: "MAT1620",
				Inicio:     viernesBloqueB,
				Fin:        viernesBloqueB.Add(duracionBloque),
				Libre:      true,
			},
			{
				ID:         "disp-3",
				TutorID:    "22222222-2222-2222-2222-222222222222",
				RamoCodigo: "MAT1620",
				Inicio:     juevesBloqueE,
				Fin:        juevesBloqueE.Add(duracionBloque),
				Libre:      true,
			},
			{
				ID:         "disp-4",
				TutorID:    "33333333-3333-3333-3333-333333333333",
				RamoCodigo: "FIS1514",
				Inicio:     martesBloqueD,
				Fin:        martesBloqueD.Add(duracionBloque),
				Libre:      true,
			},
		},
	}
}

// proximoBloqueChile calcula el próximo inicio futuro de un bloque universitario en UTC.
func proximoBloqueChile(diaSemana time.Weekday, hora, minuto int) time.Time {
	loc := zonaChile()
	ahora := time.Now().In(loc)
	diasHasta := (int(diaSemana) - int(ahora.Weekday()) + 7) % 7
	candidato := time.Date(ahora.Year(), ahora.Month(), ahora.Day()+diasHasta, hora, minuto, 0, 0, loc)
	if !candidato.After(ahora) {
		candidato = candidato.AddDate(0, 0, 7)
	}
	return candidato.UTC()
}

func (m *MockBFFClient) ListarRamos(_ context.Context) ([]domain.Ramo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copia := make([]domain.Ramo, len(m.ramos))
	copy(copia, m.ramos)
	return copia, nil
}

func (m *MockBFFClient) BuscarTutores(_ context.Context, ramoCodigo string) ([]domain.TutorResumen, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var resultado []domain.TutorResumen
	for _, t := range m.tutores {
		for _, r := range t.Ramos {
			if r.Activo && strings.EqualFold(r.Ramo.Codigo, ramoCodigo) {
				resultado = append(resultado, t)
				break
			}
		}
	}
	return resultado, nil
}

func (m *MockBFFClient) BuscarDisponibilidad(_ context.Context, ramoCodigo string, desde, hasta time.Time) ([]domain.BloqueDisponibilidad, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var resultado []domain.BloqueDisponibilidad
	for _, b := range m.disponibilidad {
		if !b.Libre || !strings.EqualFold(b.RamoCodigo, ramoCodigo) {
			continue
		}
		if !b.Inicio.Before(desde) && b.Inicio.Before(hasta) {
			resultado = append(resultado, b)
		}
	}
	return resultado, nil
}

func (m *MockBFFClient) VerDisponibilidad(_ context.Context, tutorID string, desde, hasta time.Time) ([]domain.BloqueDisponibilidad, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var resultado []domain.BloqueDisponibilidad
	for _, b := range m.disponibilidad {
		if b.TutorID != tutorID {
			continue
		}
		if !b.Inicio.Before(desde) && b.Inicio.Before(hasta) {
			resultado = append(resultado, b)
		}
	}
	return resultado, nil
}

func (m *MockBFFClient) CrearReserva(_ context.Context, tutorID, estudianteID, ramoCodigo string, inicio time.Time) (domain.ReservaCreada, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tutorID == estudianteID {
		return domain.ReservaCreada{}, fmt.Errorf("%w: no puedes reservarte a ti mismo", domain.ErrServicioExterno)
	}

	idxCasilla := -1
	for i, b := range m.disponibilidad {
		if b.TutorID == tutorID && strings.EqualFold(b.RamoCodigo, ramoCodigo) && b.Inicio.Equal(inicio.UTC()) && b.Libre {
			idxCasilla = i
			break
		}
	}
	if idxCasilla == -1 {
		return domain.ReservaCreada{}, fmt.Errorf("%w: no hay una casilla libre para ese tutor, ramo y horario", domain.ErrServicioExterno)
	}

	m.disponibilidad[idxCasilla].Libre = false
	m.contadorID++
	reserva := domain.ReservaCreada{
		ID:           fmt.Sprintf("sesion-mock-%d", m.contadorID),
		TutorID:      tutorID,
		EstudianteID: estudianteID,
		RamoCodigo:   strings.ToUpper(ramoCodigo),
		Inicio:       inicio.UTC(),
		Fin:          inicio.UTC().Add(duracionBloque),
		Estado:       "PENDIENTE",
		CreadaEn:     time.Now().UTC(),
	}
	m.reservas = append(m.reservas, reserva)
	return reserva, nil
}

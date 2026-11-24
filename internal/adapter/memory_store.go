package adapter

import (
	"container/list"
	"sync"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

const (
	defaultMaxConversaciones = 1000
	defaultTTLConversacion   = 30 * time.Minute
	defaultMaxMensajes       = 30
)

type lruEntry struct {
	clave    string
	mensajes []domain.MensajeChat
	expiraEn time.Time
}

// LRUConversationStore implementa domain.ConversationStore con expulsión LRU + TTL en O(1),
// protegiendo la memoria del pod en Kubernetes contra crecimiento ilimitado (OOM).
type LRUConversationStore struct {
	mu          sync.Mutex
	maxEntradas int
	maxMensajes int
	ttl         time.Duration
	now         func() time.Time
	orden       *list.List
	elementos   map[string]*list.Element
}

func NewLRUConversationStore(maxEntradas int, ttl time.Duration, maxMensajes int) *LRUConversationStore {
	if maxEntradas <= 0 {
		maxEntradas = defaultMaxConversaciones
	}
	if ttl <= 0 {
		ttl = defaultTTLConversacion
	}
	if maxMensajes <= 0 {
		maxMensajes = defaultMaxMensajes
	}
	return &LRUConversationStore{
		maxEntradas: maxEntradas,
		maxMensajes: maxMensajes,
		ttl:         ttl,
		now:         time.Now,
		orden:       list.New(),
		elementos:   make(map[string]*list.Element, maxEntradas),
	}
}

func claveConversacion(usuarioID, conversacionID string) string {
	return usuarioID + ":" + conversacionID
}

func (s *LRUConversationStore) Obtener(usuarioID, conversacionID string) []domain.MensajeChat {
	s.mu.Lock()
	defer s.mu.Unlock()

	clave := claveConversacion(usuarioID, conversacionID)
	elem, ok := s.elementos[clave]
	if !ok {
		return nil
	}

	entry := elem.Value.(*lruEntry)
	if !s.now().Before(entry.expiraEn) {
		s.orden.Remove(elem)
		delete(s.elementos, clave)
		return nil
	}

	s.orden.MoveToFront(elem)
	copia := make([]domain.MensajeChat, len(entry.mensajes))
	copy(copia, entry.mensajes)
	return copia
}

func (s *LRUConversationStore) Guardar(usuarioID, conversacionID string, mensajes []domain.MensajeChat) {
	s.mu.Lock()
	defer s.mu.Unlock()

	clave := claveConversacion(usuarioID, conversacionID)
	recortados := recortarHistorialSeguro(mensajes, s.maxMensajes)
	expira := s.now().Add(s.ttl)

	if elem, ok := s.elementos[clave]; ok {
		entry := elem.Value.(*lruEntry)
		entry.mensajes = recortados
		entry.expiraEn = expira
		s.orden.MoveToFront(elem)
		return
	}

	elem := s.orden.PushFront(&lruEntry{
		clave:    clave,
		mensajes: recortados,
		expiraEn: expira,
	})
	s.elementos[clave] = elem

	if s.orden.Len() > s.maxEntradas {
		masViejo := s.orden.Back()
		if masViejo != nil {
			s.orden.Remove(masViejo)
			delete(s.elementos, masViejo.Value.(*lruEntry).clave)
		}
	}
}

// recortarHistorialSeguro corta siempre al inicio de un turno RolUsuario para no dejar
// llamadas functionCall o respuestas functionResponse huérfanas que Gemini rechazaría con 400.
func recortarHistorialSeguro(mensajes []domain.MensajeChat, limite int) []domain.MensajeChat {
	if len(mensajes) <= limite {
		copia := make([]domain.MensajeChat, len(mensajes))
		copy(copia, mensajes)
		return copia
	}

	inicio := len(mensajes) - limite
	encontrado := -1
	for i := inicio; i < len(mensajes); i++ {
		if mensajes[i].Rol == domain.RolUsuario {
			encontrado = i
			break
		}
	}
	if encontrado == -1 {
		for i := inicio - 1; i >= 0; i-- {
			if mensajes[i].Rol == domain.RolUsuario {
				encontrado = i
				break
			}
		}
	}
	if encontrado == -1 {
		encontrado = 0
	}

	copia := make([]domain.MensajeChat, len(mensajes)-encontrado)
	copy(copia, mensajes[encontrado:])
	return copia
}

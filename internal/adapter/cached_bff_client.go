package adapter

import (
	"context"
	"sync"
	"time"

	"github.com/tutormatch/agente/internal/domain"
)

// CachedBFFClient aplica el Patrón Decorator sobre domain.BFFClient para cachear el catálogo
// de ramos con TTL y evitar viajes de red repetidos al BFF sin modificar HTTPBFFClient (OCP).
type CachedBFFClient struct {
	domain.BFFClient
	ttl      time.Duration
	now      func() time.Time
	mu       sync.RWMutex
	ramos    []domain.Ramo
	expiraEn time.Time
}

func NewCachedBFFClient(base domain.BFFClient, ttl time.Duration) *CachedBFFClient {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &CachedBFFClient{
		BFFClient: base,
		ttl:       ttl,
		now:       time.Now,
	}
}

func (c *CachedBFFClient) ListarRamos(ctx context.Context) ([]domain.Ramo, error) {
	c.mu.RLock()
	if len(c.ramos) > 0 && c.now().Before(c.expiraEn) {
		copia := make([]domain.Ramo, len(c.ramos))
		copy(copia, c.ramos)
		c.mu.RUnlock()
		return copia, nil
	}
	c.mu.RUnlock()

	ramos, err := c.BFFClient.ListarRamos(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.ramos = make([]domain.Ramo, len(ramos))
	copy(c.ramos, ramos)
	c.expiraEn = c.now().Add(c.ttl)
	c.mu.Unlock()

	copia := make([]domain.Ramo, len(ramos))
	copy(copia, ramos)
	return copia, nil
}

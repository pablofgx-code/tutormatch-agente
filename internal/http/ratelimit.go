package httpadapter

import (
	"container/list"
	"sync"
	"time"
)

// RateLimiter abstrae el algoritmo de limitación de peticiones (DIP).
type RateLimiter interface {
	Allow(key string) bool
}

type bucketEntry struct {
	key        string
	tokens     float64
	ultimoPaso time.Time
}

// TokenBucketLimiter implementa Token Bucket en O(1) con expulsión LRU para acotar memoria.
type TokenBucketLimiter struct {
	mu         sync.Mutex
	ratePerSec float64
	burst      float64
	maxKeys    int
	now        func() time.Time
	orden      *list.List
	buckets    map[string]*list.Element
}

// NewTokenBucketLimiter permite `requests` peticiones por cada `ventana`, con ráfaga `burst`.
func NewTokenBucketLimiter(requests int, ventana time.Duration, burst int, maxKeys int) *TokenBucketLimiter {
	if requests <= 0 {
		requests = 10
	}
	if ventana <= 0 {
		ventana = time.Minute
	}
	if burst <= 0 {
		burst = 5
	}
	if maxKeys <= 0 {
		maxKeys = 5000
	}
	return &TokenBucketLimiter{
		ratePerSec: float64(requests) / ventana.Seconds(),
		burst:      float64(burst),
		maxKeys:    maxKeys,
		now:        time.Now,
		orden:      list.New(),
		buckets:    make(map[string]*list.Element),
	}
}

func (l *TokenBucketLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	ahora := l.now()
	if elem, ok := l.buckets[key]; ok {
		b := elem.Value.(*bucketEntry)
		transcurrido := ahora.Sub(b.ultimoPaso).Seconds()
		if transcurrido > 0 {
			b.tokens += transcurrido * l.ratePerSec
			if b.tokens > l.burst {
				b.tokens = l.burst
			}
			b.ultimoPaso = ahora
		}
		l.orden.MoveToFront(elem)
		if b.tokens < 1.0 {
			return false
		}
		b.tokens -= 1.0
		return true
	}

	b := &bucketEntry{
		key:        key,
		tokens:     l.burst - 1.0,
		ultimoPaso: ahora,
	}
	elem := l.orden.PushFront(b)
	l.buckets[key] = elem

	if l.orden.Len() > l.maxKeys {
		viejo := l.orden.Back()
		if viejo != nil {
			l.orden.Remove(viejo)
			delete(l.buckets, viejo.Value.(*bucketEntry).key)
		}
	}
	return true
}

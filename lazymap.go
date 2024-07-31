package lazymap

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Map is a lazy loaded map.
// If a key doesn't have a value, it will call the lazy loading method to initialize one.
type Map[K comparable, V any] struct {

	// Every time the LoadOrCtor method is called, it will reset the timer with the Lifetime duration.
	// If zero, it means unlimited lifetime.
	Lifetime time.Duration

	// Whenever the value be deleted, it will call the OnDelete method.
	// You can do any cleanup actions.
	OnDelete func(key K, value V)

	mu sync.Mutex
	m  map[K]*entity[V]
}

type entity[V any] struct {
	wg     sync.WaitGroup
	val    V
	err    error
	timer  *time.Timer
	ctx    context.Context
	cacenl context.CancelFunc
}

// New returns a Map with lifetime duration.
func New[K comparable, V any](lifetime time.Duration) *Map[K, V] {
	return &Map[K, V]{
		Lifetime: lifetime,
	}
}

type ctorFunc[K comparable, V any] func(context.Context, K) (V, error)

// ErrCtorNotProvided lazy loading constructor not provided error
var ErrCtorNotProvided = errors.New("constructor not provided")

// LoadOrCtor returns the value for the key if it exists.
// Otherwise, it will call the constructor and return its value.
// If the constructor returns an error, the value will not be stored in the cache.
func (m *Map[K, V]) LoadOrCtor(ctx context.Context, key K, fn ctorFunc[K, V]) (V, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var value V
	if fn == nil {
		return value, ErrCtorNotProvided
	}

	m.mu.Lock()
	if m.m == nil {
		m.m = make(map[K]*entity[V])
	}

	if e, hit := m.m[key]; hit {
		if e.timer != nil {
			e.timer.Reset(m.Lifetime)
		}
		m.mu.Unlock()
		e.wg.Wait()
		return e.val, e.err
	}

	e := new(entity[V])
	// e.ctx only cancelled when entry deleted from Map
	e.ctx, e.cacenl = context.WithCancel(context.Background())
	e.wg.Add(1)
	m.m[key] = e
	m.mu.Unlock()

	e.val, e.err = fn(ctx, key)

	if e.err != nil {
		m.mu.Lock()
		delete(m.m, key)
		m.mu.Unlock()
	} else if m.Lifetime != 0 {
		e.timer = time.NewTimer(m.Lifetime)
		go m.observeEntry(key, e)
	}

	e.wg.Done()

	return e.val, e.err
}

func (m *Map[K, V]) observeEntry(key K, e *entity[V]) {
	select {
	case <-e.timer.C:
		m.Delete(key)
	case <-e.ctx.Done():
	}
	e.timer.Stop()
}

// Delete deletes the value for a key.
func (m *Map[K, V]) Delete(key K) {
	m.mu.Lock()

	e, exist := m.m[key]
	if !exist {
		m.mu.Unlock()
		return
	}

	e.cacenl()
	delete(m.m, key)
	m.mu.Unlock()

	if m.OnDelete != nil {
		m.OnDelete(key, e.val)
	}
}

package lazymap

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Map is a lazy loaded map.
// If a key cannot get a value, it will call the lazy loading method to initialize
type Map struct {

	// Everytime calling the LoadOrCtor method will reset the timer with Lifetime duration.
	// If zero, means unlimit lifetime
	Lifetime time.Duration

	// When the value has been deleted, it will call the OnDelete method.
	// You can do any cleanup actions.
	OnDelete func(key interface{}, value interface{})

	mu sync.Mutex
	m  map[interface{}]*entity
}

type entity struct {
	wg     sync.WaitGroup
	val    interface{}
	err    error
	timer  *time.Timer
	ctx    context.Context
	cacenl context.CancelFunc
}

// New returns a Map with lifetime duration.
func New(lifetime time.Duration) *Map {
	return &Map{
		Lifetime: lifetime,
	}
}

type ctorFunc func(context.Context, interface{}) (interface{}, error)

// ErrCtorNotProvided lazy loading constructor not provided error
var ErrCtorNotProvided = errors.New("constructor not provided")

// LoadOrCtor returns the value for the key if exist.
// Otherwise, it will call the constructor and returns the its value.
// If the constructor returns error, the value will not be stored in the cache.
func (m *Map) LoadOrCtor(ctx context.Context, key interface{}, fn ctorFunc) (interface{}, error) {

	if ctx == nil {
		ctx = context.Background()
	}

	if fn == nil {
		return nil, ErrCtorNotProvided
	}

	m.mu.Lock()
	if m.m == nil {
		m.m = make(map[interface{}]*entity)
	}

	if e, hit := m.m[key]; hit {
		if e.timer != nil {
			e.timer.Reset(m.Lifetime)
		}
		m.mu.Unlock()
		e.wg.Wait()
		return e.val, e.err
	}

	e := new(entity)
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

func (m *Map) observeEntry(key interface{}, e *entity) {
	select {
	case <-e.timer.C:
		m.Delete(key)
	case <-e.ctx.Done():
	}
	e.timer.Stop()
}

// Delete delete the value for a key.
func (m *Map) Delete(key interface{}) {
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

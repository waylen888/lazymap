package lazymap

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCtorNotProvided is returned by LoadOrCtor when no constructor is supplied.
var ErrCtorNotProvided = errors.New("lazymap: constructor not provided")

// Constructor lazily builds the value for a key. It is invoked at most once per
// stored key (concurrent callers for the same missing key share a single call).
// If it returns an error, the value is not cached and the error is returned to
// every waiting caller.
type Constructor[K comparable, V any] func(ctx context.Context, key K) (V, error)

// Map is a thread-safe, lazily populated map.
//
// When a key is missing, LoadOrCtor calls a Constructor to build the value and
// stores it. Entries optionally expire after Lifetime of inactivity, at which
// point OnDelete is invoked so callers can release the underlying resource.
//
// The zero Map is ready to use (with unlimited lifetime and no OnDelete hook).
// Lifetime and OnDelete must be configured before the Map is first used and not
// mutated concurrently afterwards.
type Map[K comparable, V any] struct {
	// Lifetime is how long an entry survives without being accessed. Every
	// successful LoadOrCtor or Load resets the timer. A zero Lifetime means
	// entries never expire.
	Lifetime time.Duration

	// OnDelete, if set, is called exactly once after an entry is removed —
	// whether by Delete or by lifetime expiry — giving callers a chance to
	// clean up the value (e.g. close a connection). It is never called for
	// entries whose constructor failed.
	OnDelete func(key K, value V)

	mu sync.Mutex
	m  map[K]*entry[V]
}

type entry[V any] struct {
	// wg is held (count 1) while the constructor runs. Waiters block on it
	// before reading val/err. A directly stored entry leaves wg at zero.
	wg sync.WaitGroup

	val V
	err error

	// timer fires after Lifetime to evict the entry. nil when Lifetime == 0.
	timer *time.Timer

	// deleted is set under mu once the entry has been removed from the map,
	// so an in-flight constructor knows ownership of cleanup has passed on.
	deleted bool
}

// New returns a Map whose entries expire after lifetime of inactivity. A zero
// lifetime disables expiry.
func New[K comparable, V any](lifetime time.Duration) *Map[K, V] {
	return &Map[K, V]{Lifetime: lifetime}
}

// LoadOrCtor returns the value stored for key, constructing and storing it with
// fn if absent. Concurrent calls for the same missing key invoke fn only once;
// the others wait and receive the same result. A failed construction is not
// cached. A nil ctx is treated as context.Background().
func (m *Map[K, V]) LoadOrCtor(ctx context.Context, key K, fn Constructor[K, V]) (V, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var zero V
	if fn == nil {
		return zero, ErrCtorNotProvided
	}

	m.mu.Lock()
	if m.m == nil {
		m.m = make(map[K]*entry[V])
	}

	if e, hit := m.m[key]; hit {
		if e.timer != nil {
			e.timer.Reset(m.Lifetime)
		}
		m.mu.Unlock()
		e.wg.Wait()
		return e.val, e.err
	}

	e := &entry[V]{}
	e.wg.Add(1)
	m.m[key] = e
	m.mu.Unlock()

	val, err := fn(ctx, key)

	m.mu.Lock()
	e.val, e.err = val, err
	switch {
	case err != nil:
		// Failed construction is never cached. Remove ourselves if we are
		// still the entry registered for key (Delete may have beaten us).
		if cur, ok := m.m[key]; ok && cur == e {
			delete(m.m, key)
			e.deleted = true
		}
		m.mu.Unlock()
		e.wg.Done()
		return val, err

	case e.deleted:
		// Delete raced in while we were constructing: it has already removed
		// us from the map and is waiting on wg to run OnDelete on val. Just
		// finish and hand cleanup over to it.
		m.mu.Unlock()
		e.wg.Done()
		return val, err

	default:
		if m.Lifetime != 0 {
			e.timer = time.AfterFunc(m.Lifetime, func() { m.delete(key, e) })
		}
		m.mu.Unlock()
		e.wg.Done()
		return val, err
	}
}

// Load returns the value stored for key, if present, and resets its lifetime.
// It never invokes a constructor. The boolean reports whether a usable value
// was found; in-flight constructions are awaited and a failed one reports false.
func (m *Map[K, V]) Load(key K) (V, bool) {
	m.mu.Lock()
	e, ok := m.m[key]
	if !ok {
		m.mu.Unlock()
		var zero V
		return zero, false
	}
	if e.timer != nil {
		e.timer.Reset(m.Lifetime)
	}
	m.mu.Unlock()

	e.wg.Wait()
	if e.err != nil {
		var zero V
		return zero, false
	}
	return e.val, true
}

// Len returns the number of entries currently registered, including any whose
// construction is still in flight.
func (m *Map[K, V]) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.m)
}

// Range calls f for each successfully constructed entry. Iteration stops early
// if f returns false. f operates on a snapshot taken when Range is called, so it
// may safely call other Map methods; entries added or removed concurrently may
// or may not be observed. In-flight entries are awaited; failed ones are skipped.
func (m *Map[K, V]) Range(f func(key K, value V) bool) {
	m.mu.Lock()
	keys := make([]K, 0, len(m.m))
	entries := make([]*entry[V], 0, len(m.m))
	for k, e := range m.m {
		keys = append(keys, k)
		entries = append(entries, e)
	}
	m.mu.Unlock()

	for i, e := range entries {
		e.wg.Wait()
		if e.err != nil {
			continue
		}
		if !f(keys[i], e.val) {
			return
		}
	}
}

// Delete removes the entry for key and reports whether one was present. If the
// entry exists, OnDelete is invoked after its construction (if any) completes,
// unless that construction failed.
func (m *Map[K, V]) Delete(key K) bool {
	return m.delete(key, nil)
}

// delete removes key. When want is non-nil the entry is removed only if it is
// the exact one want points to; the lifetime timer uses this to avoid evicting
// a newer entry that replaced an expired one for the same key.
func (m *Map[K, V]) delete(key K, want *entry[V]) bool {
	m.mu.Lock()
	e, ok := m.m[key]
	if !ok || (want != nil && e != want) {
		m.mu.Unlock()
		return false
	}
	delete(m.m, key)
	e.deleted = true
	if e.timer != nil {
		e.timer.Stop()
	}
	m.mu.Unlock()

	// Wait for any in-flight constructor so val is valid before cleanup.
	e.wg.Wait()
	if e.err == nil && m.OnDelete != nil {
		m.OnDelete(key, e.val)
	}
	return true
}

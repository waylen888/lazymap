package lazymap_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waylen888/lazymap"
)

// Test_ConcurrentLoadAndExpiry hammers a short-lived map from many goroutines
// while entries expire underneath them. It is a regression test for the data
// race between setting an entry's timer and resetting it on a concurrent hit;
// run with -race.
func Test_ConcurrentLoadAndExpiry(t *testing.T) {
	m := lazymap.New[int, int](time.Millisecond)
	m.OnDelete = func(int, int) {}
	ctor := func(_ context.Context, k int) (int, error) { return k, nil }

	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				key := i % 8
				if v, err := m.LoadOrCtor(context.Background(), key, ctor); err != nil || v != key {
					t.Errorf("LoadOrCtor(%d) = %d, %v", key, v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// Test_DeleteDuringConstruction verifies that deleting a key whose constructor
// is still running waits for it to finish and then runs OnDelete exactly once
// on the freshly built value — i.e. the resource is not leaked and OnDelete is
// never handed a zero value.
func Test_DeleteDuringConstruction(t *testing.T) {
	m := lazymap.New[string, string](0)

	var mu sync.Mutex
	var released []string
	m.OnDelete = func(_ string, value string) {
		mu.Lock()
		released = append(released, value)
		mu.Unlock()
	}

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		m.LoadOrCtor(context.Background(), "k", func(context.Context, string) (string, error) {
			close(started)
			<-release
			return "built", nil
		})
	}()

	<-started // constructor is in flight and the entry is registered

	deleted := make(chan bool, 1)
	go func() { deleted <- m.Delete("k") }()

	close(release) // let the constructor finish

	if !<-deleted {
		t.Fatal("Delete returned false for an existing key")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(released) != 1 || released[0] != "built" {
		t.Fatalf("OnDelete calls = %v, want exactly [built]", released)
	}
	if n := m.Len(); n != 0 {
		t.Fatalf("Len after delete = %d, want 0", n)
	}
}

// Test_ConcurrentDeleteOnDeleteOnce ensures OnDelete fires exactly once per
// value when expiry and explicit Delete race.
func Test_ConcurrentDeleteOnDeleteOnce(t *testing.T) {
	var calls atomic.Int64
	m := lazymap.New[string, int](time.Millisecond)
	m.OnDelete = func(string, int) { calls.Add(1) }

	for round := 0; round < 200; round++ {
		_, err := m.LoadOrCtor(context.Background(), "k", func(context.Context, string) (int, error) {
			return round, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); m.Delete("k") }()
		time.Sleep(time.Millisecond) // let the timer race the Delete
		wg.Wait()
		m.Delete("k") // idempotent no-op if already gone
	}

	if got := calls.Load(); got > 200 {
		t.Fatalf("OnDelete fired %d times, want at most 200 (once per stored value)", got)
	}
}

func Test_LoadAndRange(t *testing.T) {
	m := lazymap.New[string, int](0)
	if _, ok := m.Load("missing"); ok {
		t.Fatal("Load found a missing key")
	}

	for i := 0; i < 5; i++ {
		k := strconv.Itoa(i)
		m.LoadOrCtor(context.Background(), k, func(_ context.Context, key string) (int, error) {
			n, _ := strconv.Atoi(key)
			return n, nil
		})
	}

	if v, ok := m.Load("3"); !ok || v != 3 {
		t.Fatalf("Load(3) = %d, %v", v, ok)
	}
	if n := m.Len(); n != 5 {
		t.Fatalf("Len = %d, want 5", n)
	}

	sum := 0
	m.Range(func(_ string, v int) bool {
		sum += v
		return true
	})
	if sum != 0+1+2+3+4 {
		t.Fatalf("Range sum = %d, want 10", sum)
	}

	// Early stop.
	count := 0
	m.Range(func(string, int) bool {
		count++
		return false
	})
	if count != 1 {
		t.Fatalf("Range visited %d entries after stop, want 1", count)
	}
}

func Test_FailedConstructionNotCachedNoOnDelete(t *testing.T) {
	var onDelete atomic.Int64
	m := lazymap.New[string, int](0)
	m.OnDelete = func(string, int) { onDelete.Add(1) }

	boom := errors.New("boom")
	_, err := m.LoadOrCtor(context.Background(), "k", func(context.Context, string) (int, error) {
		return 0, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := m.Len(); n != 0 {
		t.Fatalf("failed construction was cached: Len = %d", n)
	}
	if n := onDelete.Load(); n != 0 {
		t.Fatalf("OnDelete fired %d times for a failed construction", n)
	}
}

func Test_CapacityEvictsLRU(t *testing.T) {
	var mu sync.Mutex
	var evicted []int
	m := &lazymap.Map[int, int]{Capacity: 3}
	m.OnDelete = func(_ int, v int) {
		mu.Lock()
		evicted = append(evicted, v)
		mu.Unlock()
	}
	ctor := func(_ context.Context, k int) (int, error) { return k, nil }

	// Fill 0,1,2.
	for k := 0; k < 3; k++ {
		m.LoadOrCtor(context.Background(), k, ctor)
	}
	// Touch 0 so 1 becomes the least-recently-used.
	if _, ok := m.Load(0); !ok {
		t.Fatal("Load(0) missing")
	}
	// Insert 3 -> evicts 1.
	m.LoadOrCtor(context.Background(), 3, ctor)

	if n := m.Len(); n != 3 {
		t.Fatalf("Len = %d, want 3", n)
	}
	if _, ok := m.Load(1); ok {
		t.Fatal("key 1 should have been evicted")
	}
	for _, k := range []int{0, 2, 3} {
		if _, ok := m.Load(k); !ok {
			t.Fatalf("key %d should still be present", k)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(evicted) != 1 || evicted[0] != 1 {
		t.Fatalf("evicted = %v, want [1]", evicted)
	}
}

func Test_CapacityConcurrentBounded(t *testing.T) {
	const capacity = 16
	m := &lazymap.Map[int, int]{Capacity: capacity}
	m.OnDelete = func(int, int) {}
	ctor := func(_ context.Context, k int) (int, error) { return k, nil }

	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				m.LoadOrCtor(context.Background(), (g*5000+i)%256, ctor)
			}
		}(g)
	}
	wg.Wait()

	// All constructions are finished, so the soft bound is now exact.
	if n := m.Len(); n != capacity {
		t.Fatalf("Len = %d, want %d", n, capacity)
	}
}

func Test_DeleteIf_MatchAndMismatch(t *testing.T) {
	var mu sync.Mutex
	var released []*int
	m := lazymap.New[string, *int](0)
	m.OnDelete = func(_ string, v *int) {
		mu.Lock()
		released = append(released, v)
		mu.Unlock()
	}

	ctorFor := func(p *int) lazymap.Constructor[string, *int] {
		return func(context.Context, string) (*int, error) { return p, nil }
	}

	v1 := new(int)
	got, _ := m.LoadOrCtor(context.Background(), "k", ctorFor(v1))
	if got != v1 {
		t.Fatal("unexpected v1")
	}

	// Deleting a different identity must not touch the stored value.
	other := new(int)
	if m.DeleteIf("k", func(cur *int) bool { return cur == other }) {
		t.Fatal("DeleteIf removed an entry whose value did not match")
	}
	if _, ok := m.Load("k"); !ok {
		t.Fatal("entry wrongly removed")
	}

	// Deleting by the real identity must remove it and fire OnDelete once.
	if !m.DeleteIf("k", func(cur *int) bool { return cur == v1 }) {
		t.Fatal("DeleteIf failed to remove the matching entry")
	}
	if _, ok := m.Load("k"); ok {
		t.Fatal("entry not removed")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(released) != 1 || released[0] != v1 {
		t.Fatalf("OnDelete = %v, want exactly [v1]", released)
	}
}

// Test_DeleteIf_NotFooledByRebuild reproduces the churn race the API exists to
// prevent: a stale holder of v1 must not delete the rebuilt v2.
func Test_DeleteIf_NotFooledByRebuild(t *testing.T) {
	var onDelete atomic.Int64
	m := lazymap.New[string, *int](0)
	m.OnDelete = func(string, *int) { onDelete.Add(1) }

	v1 := new(int)
	v2 := new(int)

	m.LoadOrCtor(context.Background(), "k", func(context.Context, string) (*int, error) { return v1, nil })
	// v1 breaks: an owner removes it...
	if !m.DeleteIf("k", func(cur *int) bool { return cur == v1 }) {
		t.Fatal("first DeleteIf should have removed v1")
	}
	// ...and a healthy v2 is rebuilt under the same key.
	m.LoadOrCtor(context.Background(), "k", func(context.Context, string) (*int, error) { return v2, nil })

	// A late holder of v1 tries to evict it again — must be a no-op on v2.
	if m.DeleteIf("k", func(cur *int) bool { return cur == v1 }) {
		t.Fatal("stale DeleteIf must not remove the rebuilt value")
	}
	if got, ok := m.Load("k"); !ok || got != v2 {
		t.Fatalf("v2 should remain, got %v ok=%v", got, ok)
	}
	if n := onDelete.Load(); n != 1 {
		t.Fatalf("OnDelete fired %d times, want 1 (only v1)", n)
	}
}

func Test_DeleteIf_InFlightNotMatched(t *testing.T) {
	var onDelete atomic.Int64
	m := lazymap.New[string, int](0)
	m.OnDelete = func(string, int) { onDelete.Add(1) }

	if m.DeleteIf("missing", nil) {
		t.Fatal("DeleteIf on a missing key returned true")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		m.LoadOrCtor(context.Background(), "k", func(context.Context, string) (int, error) {
			close(started)
			<-release
			return 7, nil
		})
		close(done)
	}()
	<-started // constructor in flight; e.val is still the zero value

	predCalled := false
	if m.DeleteIf("k", func(int) bool { predCalled = true; return true }) {
		t.Fatal("DeleteIf matched an in-flight entry")
	}
	if predCalled {
		t.Fatal("pred must not run against an in-flight (zero) value")
	}

	close(release)
	<-done
	if n := onDelete.Load(); n != 0 {
		t.Fatalf("OnDelete fired %d times for an untouched in-flight entry", n)
	}
	// Once ready it can be deleted.
	if !m.DeleteIf("k", nil) {
		t.Fatal("DeleteIf(nil) failed on a ready entry")
	}
}

func Test_DeleteIf_CapacityConsistent(t *testing.T) {
	m := &lazymap.Map[int, int]{Capacity: 4}
	m.OnDelete = func(int, int) {}
	ctor := func(_ context.Context, k int) (int, error) { return k, nil }

	for k := 0; k < 4; k++ {
		m.LoadOrCtor(context.Background(), k, ctor)
	}
	if !m.DeleteIf(1, func(v int) bool { return v == 1 }) {
		t.Fatal("DeleteIf(1) failed")
	}
	// Reinserting beyond capacity must still evict cleanly via the LRU list.
	for k := 4; k < 8; k++ {
		m.LoadOrCtor(context.Background(), k, ctor)
	}
	if n := m.Len(); n != 4 {
		t.Fatalf("Len = %d, want 4", n)
	}
}

func BenchmarkLoadOrCtor_Hit(b *testing.B) {
	m := lazymap.New[string, int](0)
	ctor := func(context.Context, string) (int, error) { return 42, nil }
	m.LoadOrCtor(context.Background(), "k", ctor)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			m.LoadOrCtor(context.Background(), "k", ctor)
		}
	})
}

func BenchmarkLoadOrCtor_Miss(b *testing.B) {
	ctor := func(_ context.Context, k int) (int, error) { return k, nil }

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		m := lazymap.New[int, int](0)
		i := 0
		for pb.Next() {
			m.LoadOrCtor(context.Background(), i, ctor)
			i++
		}
	})
}

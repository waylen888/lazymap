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

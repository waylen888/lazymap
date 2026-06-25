package lazymap_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/waylen888/lazymap"
)

// ExampleMap_LoadOrCtor shows that concurrent requests for the same missing key
// run the constructor only once and share its result.
func ExampleMap_LoadOrCtor() {
	m := lazymap.New[string, int](0)

	var calls atomic.Int64
	ctor := func(ctx context.Context, key string) (int, error) {
		calls.Add(1)
		return len(key), nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.LoadOrCtor(context.Background(), "hello", ctor)
		}()
	}
	wg.Wait()

	v, _ := m.LoadOrCtor(context.Background(), "hello", ctor)
	fmt.Println("value:", v)
	fmt.Println("constructor calls:", calls.Load())
	// Output:
	// value: 5
	// constructor calls: 1
}

// ExampleMap_OnDelete shows the cleanup hook firing on Delete.
func ExampleMap_OnDelete() {
	m := lazymap.New[string, string](0)
	m.OnDelete = func(key, value string) {
		fmt.Printf("released %s=%s\n", key, value)
	}

	m.LoadOrCtor(context.Background(), "k", func(context.Context, string) (string, error) {
		return "v", nil
	})
	m.Delete("k")
	// Output:
	// released k=v
}

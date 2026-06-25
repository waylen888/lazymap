// Package lazymap implements a thread-safe map whose values are produced on
// demand by a constructor and optionally expire after a period of inactivity.
//
// It behaves like a combination of a cache and golang.org/x/sync/singleflight:
// the first caller to request a missing key runs the constructor while any
// concurrent callers for the same key block and share the result. Successful
// values are cached; failed constructions are not.
//
// # Lifetime and capacity
//
// A Map created with a non-zero lifetime evicts entries that have not been
// accessed for that duration. Each LoadOrCtor and Load resets the entry's
// timer. A non-zero Capacity additionally caps the number of entries, evicting
// the least-recently-used one when exceeded. On any eviction — or on an explicit
// Delete — the OnDelete hook (if set) is invoked so the caller can release the
// underlying resource. OnDelete fires exactly once per stored value and never
// for a failed construction.
//
// # Example
//
//	m := lazymap.New[string, net.Conn](10 * time.Second)
//	m.OnDelete = func(addr string, c net.Conn) { c.Close() }
//
//	conn, err := m.LoadOrCtor(ctx, "localhost:8080",
//		func(ctx context.Context, addr string) (net.Conn, error) {
//			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
//		})
//
// All methods are safe for concurrent use. The configuration fields Lifetime
// and OnDelete must be set before first use and not mutated concurrently.
package lazymap

# lazymap

[![CI](https://github.com/waylen888/lazymap/actions/workflows/ci.yml/badge.svg)](https://github.com/waylen888/lazymap/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/waylen888/lazymap.svg)](https://pkg.go.dev/github.com/waylen888/lazymap)
[![Go Report Card](https://goreportcard.com/badge/github.com/waylen888/lazymap)](https://goreportcard.com/report/github.com/waylen888/lazymap)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A thread-safe, generic Go map whose values are built on demand by a constructor
and optionally expire after a period of inactivity.

- **Lazy loading** — a missing key is populated by a constructor you provide.
- **Single-flight** — concurrent requests for the same missing key run the
  constructor once and share the result.
- **Lifetime / TTL** — entries can expire after a configurable idle duration.
- **Capacity / LRU** — an optional bound evicts the least-recently-used entry.
- **Cleanup hook** — `OnDelete` fires exactly once per value (on expiry,
  capacity eviction or explicit delete) so you can release the resource.
- **Generic & zero-dependency** — `Map[K comparable, V any]`, standard library
  only.

## Install

```sh
go get github.com/waylen888/lazymap
```

## Example

A pooled connection map that dials lazily, reuses connections, and closes them
when they fall idle:

```go
package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/waylen888/lazymap"
)

func main() {
	// Connections expire after 10 seconds of inactivity.
	m := lazymap.New[string, net.Conn](10 * time.Second)

	// Close a connection when it expires or is deleted.
	m.OnDelete = func(addr string, conn net.Conn) {
		fmt.Printf("closing connection %s\n", addr)
		conn.Close()
	}

	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		fmt.Printf("connecting to %s\n", addr)
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}

	conn, err := m.LoadOrCtor(context.Background(), "localhost:8080", dial)
	if err != nil {
		// dial failed; nothing is cached, so the next call retries.
		return
	}

	if _, err := conn.Write([]byte("ping\n")); err != nil {
		m.Delete("localhost:8080") // triggers OnDelete -> conn.Close()
	}
}
```

## API

| Method | Description |
| --- | --- |
| `New[K, V](lifetime time.Duration) *Map[K, V]` | Create a map; zero lifetime disables expiry. (`Capacity`/`OnDelete` are set as fields.) |
| `LoadOrCtor(ctx, key, fn) (V, error)` | Return the cached value or construct it. Single-flight; failed constructions are not cached. |
| `Load(key) (V, bool)` | Return the value if present (no construction); resets its lifetime. |
| `Delete(key) bool` | Remove an entry and run `OnDelete`; reports whether it existed. |
| `DeleteIf(key, pred) bool` | Remove a constructed entry only if `pred(currentValue)` holds — evict "your" instance without racing a concurrent rebuild. |
| `Len() int` | Number of registered entries. |
| `Range(func(K, V) bool)` | Iterate a snapshot of entries; return `false` to stop. |

Configure behaviour with the exported fields `Lifetime`, `Capacity` and
`OnDelete` before first use:

```go
m := &lazymap.Map[string, net.Conn]{
	Lifetime: 10 * time.Second, // evict after 10s idle
	Capacity: 100,              // keep at most 100 connections (LRU)
	OnDelete: func(addr string, c net.Conn) { c.Close() },
}
```

## Semantics & guarantees

- All methods are safe for concurrent use.
- The constructor runs **at most once** per stored key; a returned error is
  propagated to all waiting callers and **not** cached.
- `OnDelete` is invoked **exactly once** per successfully stored value and
  **never** for a failed construction. Deleting a key whose constructor is
  still running waits for it to finish before cleaning up — the value is never
  leaked, and `OnDelete` never receives a zero value.
- TTL expiry is best-effort: a value may be returned just as its lifetime
  elapses. Reset happens on every `LoadOrCtor` and `Load`.
- `Capacity` is a soft bound with respect to in-flight constructions: an entry
  whose constructor is still running is never evicted, so concurrent loads may
  briefly exceed `Capacity`. Once they complete, the bound is exact.
- `DeleteIf` evaluates its predicate under the lock against the value stored at
  that instant, so it removes only the instance you mean. It never matches an
  in-flight entry; its predicate must be a cheap, pure check that does not call
  back into the `Map`. Typical use in a pool:

  ```go
  cli, err := pool.LoadOrCtor(ctx, key, ctor)
  if err == nil {
      if _, err = cli.Send(req); err != nil {
          pool.DeleteIf(key, func(cur *Client) bool { return cur == cli })
      }
  }
  ```

## Development

```sh
go test -race ./...        # tests under the race detector
go test -bench=. ./...     # benchmarks
golangci-lint run          # lint
```

## License

[MIT](LICENSE)

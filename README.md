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
- **Cleanup hook** — `OnDelete` fires exactly once per value (on expiry or
  explicit delete) so you can release the underlying resource.
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
| `New[K, V](lifetime time.Duration) *Map[K, V]` | Create a map; zero lifetime disables expiry. |
| `LoadOrCtor(ctx, key, fn) (V, error)` | Return the cached value or construct it. Single-flight; failed constructions are not cached. |
| `Load(key) (V, bool)` | Return the value if present (no construction); resets its lifetime. |
| `Delete(key) bool` | Remove an entry and run `OnDelete`; reports whether it existed. |
| `Len() int` | Number of registered entries. |
| `Range(func(K, V) bool)` | Iterate a snapshot of entries; return `false` to stop. |

Configure behaviour with the exported fields `Lifetime` and `OnDelete` before
first use.

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

## Development

```sh
go test -race ./...        # tests under the race detector
go test -bench=. ./...     # benchmarks
golangci-lint run          # lint
```

## License

[MIT](LICENSE)

Lazymap
========

The `lazymap` package implements a thread-safe map with lazy loading capabilities.

Example
========

Here's an example of a persistent connection map with an initialized lazy-loading connect method:

```go
func main() {
  // Initialize with a 10-second lifetime
	m := lazymap.New[string, net.Conn](time.Second * 10)

  // Define end-of-life behavior
	m.OnDelete = func(key string, conn net.Conn) {
		fmt.Printf("Closing connection %v\n", key)
		conn.Close()
	}

  // Simulate multiple goroutines accessing the network connection
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			v, err := m.LoadOrCtor(context.Background(), "localhost:8080", constructor)
			if err != nil {
				fmt.Printf("LoadOrCtor error: %v\n", err)
				return
			}
			fmt.Printf("Writing data %v\n", i)
			_, err = v.(net.Conn).Write([]byte(fmt.Sprintf("%d\n", i)))
			if err != nil {
				m.Delete("localhost:8080")
			}
		}()
	}

	select {}
}

func constructor(ctx context.Context, key string) (net.Conn, error) {
	host := key
	fmt.Printf("Connecting to %s\n", host)
	d := net.Dialer{}
	return d.DialContext(ctx, "tcp", host)
}
```
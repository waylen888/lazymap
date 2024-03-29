Lazymap
========

This provides the `lazymap` package which implements a lazy loading
thread safe map.

Example
========

The persistent connection map, with initialized connect lazy loading method.

```go

func main() {
	
  // Init with 10 seconds lifetime.
	m := lazymap.New[string, net.Conn](time.Second * 10)

  // End of life.
	m.OnDelete = func(key string, conn net.Conn) {
		fmt.Printf("Close conn %v\n", key)
		conn.Close()
	}

  // Multiple goroutine get the net connection
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			v, err := m.LoadOrCtor(context.Background(), "localhost:8080", constructor)
			if err != nil {
				fmt.Printf("LoadOrCtor err %v\n", err)
				return
			}
			fmt.Printf("Write data %v\n", i)
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
	fmt.Printf("Connect to %s\n", host)
	d := net.Dialer{}
	return d.DialContext(ctx, "tcp", host)
}

```
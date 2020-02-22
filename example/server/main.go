package main

import (
	"bufio"
	"fmt"
	"net"
)

func main() {
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Accept error %v\n", err)
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		line, _, err := r.ReadLine()
		if err != nil {
			fmt.Printf("Read line error %v\n", err)
			return
		}
		fmt.Printf("Read line %s\n", line)
	}

}

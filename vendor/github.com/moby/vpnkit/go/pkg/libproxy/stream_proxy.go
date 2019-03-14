package libproxy

import (
	"io"
	"log"
	"net"
)

// Conn defines a network connection
type Conn interface {
	net.Conn
	CloseRead() error
	CloseWrite() error
}

func proxy(client, backend Conn, quit chan struct{}) error {
	event := make(chan int64)
	var broker = func(to, from Conn) {
		written, err := io.Copy(to, from)
		if err != nil {
			log.Println("error copying:", err)
		}
		err = from.CloseRead()
		if err != nil {
			log.Println("error CloseRead from:", err)
		}
		err = to.CloseWrite()
		if err != nil {
			log.Println("error CloseWrite to:", err)
		}
		event <- written
	}

	go broker(client, backend)
	go broker(backend, client)

	var transferred int64
	for i := 0; i < 2; i++ {
		select {
		case written := <-event:
			transferred += written
		case <-quit:
			// Interrupt the two brokers and "join" them.
			backend.Close()
			for ; i < 2; i++ {
				transferred += <-event
			}
			return nil
		}
	}
	backend.Close()
	return nil
}

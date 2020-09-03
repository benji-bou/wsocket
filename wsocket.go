package wsocket

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

type Socket struct {
	twrite     chan []byte
	tread      chan []byte
	bwrite     chan []byte
	bread      chan []byte
	errc       chan error
	closeState bool

	co *websocket.Conn
}

func (c *Socket) Close() {
	log.Println("Closing WebSocket")
	c.co.Close()
	c.closeState = true
}

func (c *Socket) Read(messagType int) <-chan []byte {
	switch messagType {
	case websocket.BinaryMessage:
		return c.bread
	case websocket.TextMessage:
		return c.tread
	}
	return c.tread
}

func (c *Socket) Error() <-chan error {
	return c.errc
}

func (c *Socket) Write(messagType int) chan<- []byte {
	switch messagType {
	case websocket.BinaryMessage:
		return c.bwrite
	case websocket.TextMessage:
		return c.twrite
	}
	return c.twrite
}

func (c *Socket) concurrentRead() {
	for {

		t, b, err := c.co.ReadMessage()
		if err != nil {
			select {
			case c.errc <- err:
			default:
				log.Println(err)
			}
			return
		}
		switch t {
		case websocket.BinaryMessage:
			select {
			case c.bread <- b:
			default:
			}
		case websocket.TextMessage:
			select {
			case c.tread <- b:
			default:
			}
		}
	}
}

func (c *Socket) concurentWrite() {
	for {
		select {
		case b := <-c.bwrite:

			if err := c.co.WriteMessage(websocket.BinaryMessage, b); err != nil {
				if c.closeState == false {
					select {
					case c.errc <- err:
					default:
						log.Println("unable to send bwrite error to channel", err)
					}
					return
				}

			}
		case t := <-c.twrite:
			if err := c.co.WriteMessage(websocket.TextMessage, t); err != nil {
				if c.closeState == false {
					select {
					case c.errc <- err:
					default:
						log.Println("unable to send twrite error to channel", err)
					}
					return
				}
			}

		}

	}
}

func ConnectSocket(addr string) (*Socket, error) {
	conn, _, err := websocket.DefaultDialer.Dial(addr, nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	if err != nil {
		return nil, err
	}
	cl := &Socket{co: conn, twrite: make(chan []byte, 256), tread: make(chan []byte, 256), bwrite: make(chan []byte, 256), bread: make(chan []byte, 256), errc: make(chan error), closeState: false}
	go cl.concurentWrite()
	go cl.concurrentRead()
	return cl, nil
}

func AcceptNewSocket(w http.ResponseWriter, r *http.Request) (*Socket, error) {
	conn, err := initSocket(w, r)
	if err != nil {
		return nil, err
	}

	cl := &Socket{co: conn, twrite: make(chan []byte, 256), tread: make(chan []byte, 256), bwrite: make(chan []byte, 256), bread: make(chan []byte, 256), errc: make(chan error), closeState: false}
	go cl.concurentWrite()
	go cl.concurrentRead()
	return cl, nil
}

func initSocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return upgrader.Upgrade(w, r, nil)
}

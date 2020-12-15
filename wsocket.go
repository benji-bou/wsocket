package wsocket

import (
	"encoding"
	"encoding/json"
	"fmt"
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
	read       chan []byte
	bwrite     chan []byte
	errc       chan error
	closeState bool

	co *websocket.Conn
}

func (c *Socket) Close() {
	log.Printf("Closing WebSocket")
	c.co.Close()
	c.closeState = true
}

func (c *Socket) Read() <-chan []byte {
	return c.read
}

func (c *Socket) Error() <-chan error {
	return c.errc
}

func (c *Socket) Write(messagesType ...int) chan<- []byte {
	messageType := websocket.TextMessage
	if len(messagesType) > 0 {
		messageType = messagesType[0]
	}
	switch messageType {
	case websocket.BinaryMessage:
		return c.bwrite
	case websocket.TextMessage:
		return c.twrite
	}
	return c.twrite
}

func (c *Socket) SendMessage(i interface{}) error {
	if c.closeState == true {
		return fmt.Errorf("Socket is closed")
	}
	switch mes := i.(type) {
	case []byte:
		c.Write(websocket.BinaryMessage) <- mes
	case string:
		c.Write(websocket.TextMessage) <- []byte(mes)
	case encoding.BinaryMarshaler:
		data, err := mes.MarshalBinary()
		if err != nil {
			return fmt.Errorf("Failed to marshal into binary with err : %w\n", err)
		}
		c.Write(websocket.BinaryMessage) <- data
	case encoding.TextMarshaler:
		data, err := mes.MarshalText()
		if err != nil {
			return fmt.Errorf("Failed to marshal into text with err : %w\n", err)
		}
		c.Write(websocket.TextMessage) <- data
	default:
		data, err := json.Marshal(i)
		if err != nil {
			return fmt.Errorf("Failed to marshal into json with err : %w\n", err)
		}
		c.Write(websocket.TextMessage) <- data
	}
	return nil
}

func (c *Socket) concurrentRead() {
	for {
		_, b, err := c.co.ReadMessage()
		if err != nil {
			log.Printf("Error reading from socket: %v - Closing it\n", err)
			c.Close()
			select {
			case c.errc <- err:
			default:
			}
			return
		}
		select {
		case c.read <- b:

		}
	}
}

func (c *Socket) concurentWrite() {
	for {
		select {
		case b := <-c.bwrite:
			if err := c.co.WriteMessage(websocket.BinaryMessage, b); err != nil {
				log.Printf("Error writing to socket: %v\n", err)
				if c.closeState == false {
					select {
					case c.errc <- err:
					default:
						log.Printf("unable to send bwrite error to channel %v\n", err)
					}
				}
				return
			}
		case t := <-c.twrite:
			if err := c.co.WriteMessage(websocket.TextMessage, t); err != nil {
				log.Printf("Error writing to socket: %v\n", err)

				if c.closeState == false {
					select {
					case c.errc <- err:
					default:
						log.Printf("unable to send twrite error to channel %v\n", err)
					}
				}
				return
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
	cl := &Socket{co: conn, twrite: make(chan []byte, 256), read: make(chan []byte, 256), bwrite: make(chan []byte, 256), errc: make(chan error), closeState: false}
	go cl.concurentWrite()
	go cl.concurrentRead()
	return cl, nil
}

func AcceptNewSocket(w http.ResponseWriter, r *http.Request) (*Socket, error) {
	conn, err := initSocket(w, r)
	if err != nil {
		return nil, err
	}

	cl := &Socket{co: conn, twrite: make(chan []byte, 256), read: make(chan []byte, 256), bwrite: make(chan []byte, 256), errc: make(chan error), closeState: false}
	go cl.concurentWrite()
	go cl.concurrentRead()
	return cl, nil
}

func initSocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return upgrader.Upgrade(w, r, nil)
}

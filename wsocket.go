package wsocket

import (
	"encoding/json"
	"log"
	"net/http"

	"encoding"

	"github.com/fatih/color"
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
	color.Red("Closing WebSocket")
	c.co.Close()
	c.closeState = true
}

func (c *Socket) Read(messagesType ...int) <-chan []byte {
	messageType := websocket.TextMessage
	if len(messagesType) > 0 {
		messageType = messagesType[0]
	}
	switch messageType {
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

func (c *Socket) SendMessage(i interface{}) {
	switch mes := i.(type) {
	case []byte:
		c.Write(websocket.BinaryMessage) <- mes
	case string:
		c.Write(websocket.TextMessage) <- []byte(mes)
	case encoding.BinaryMarshaler:
		data, err := mes.MarshalBinary()
		if err != nil {
			color.Red("Failed to marshal into binary with err : %v", err)
			return
		}
		c.Write(websocket.BinaryMessage) <- data
	case encoding.TextMarshaler:
		data, err := mes.MarshalText()
		if err != nil {
			color.Red("Failed to marshal into text with err : %v", err)
			return
		}
		c.Write(websocket.TextMessage) <- data
	default:
		data, err := json.Marshal(i)
		if err != nil {
			color.Red("Failed to marshal into json with err : %v", err)
			return
		}
		c.Write(websocket.TextMessage) <- data
	}
}

func (c *Socket) concurrentRead() {
	for {

		t, b, err := c.co.ReadMessage()
		if err != nil {
			color.Red("Error reading from socket: %v", err)
			select {
			case c.errc <- err:
			default:
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
				color.Red("Error writing to socket: %v", err)
				if c.closeState == false {
					select {
					case c.errc <- err:
					default:
						color.Red("unable to send bwrite error to channel %v", err)
					}
					return
				}
			}
		case t := <-c.twrite:
			if err := c.co.WriteMessage(websocket.TextMessage, t); err != nil {
				color.Red("Error writing to socket: %v", err)

				if c.closeState == false {
					select {
					case c.errc <- err:
					default:
						color.Red("unable to send twrite error to channel %v", err)
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

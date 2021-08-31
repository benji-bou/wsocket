package wsocket

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
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

func (c *Socket) sendData(writer io.WriteCloser, data []byte, nextData <-chan []byte) error {
	writer.Write(data)
	n := len(nextData)
	for i := 0; i < n; i++ {
		writer.Write(<-nextData)
	}
	return writer.Close()
}

func (c *Socket) errAndClose(err error) {
	log.Printf("Error writing to socket: %v\n", err)
	if !c.closeState {
		select {
		case c.errc <- err:
		default:
			log.Printf("unable to send bwrite error to channel %v\n", err)
		}
	}
}

func (c *Socket) concurentWrite() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.co.Close()
	}()
	for {
		select {
		case b := <-c.bwrite:

			writer, err := c.co.NextWriter(websocket.BinaryMessage)
			if err != nil {
				c.errAndClose(err)
				return
			}
			if err := c.sendData(writer, b, c.bwrite); err != nil {
				c.errAndClose(err)
				return
			}

		case t := <-c.twrite:
			writer, err := c.co.NextWriter(websocket.TextMessage)
			if err != nil {
				c.errAndClose(err)
				return
			}
			if err := c.sendData(writer, t, c.twrite); err != nil {
				c.errAndClose(err)
				return
			}
		case <-ticker.C:
			c.co.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.co.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.errAndClose(err)
				return
			}
		}
	}
}

func ConnectSocket(addr string, header http.Header) (*Socket, error) {
	conn, resp, err := websocket.DefaultDialer.Dial(addr, header)
	if err == websocket.ErrBadHandshake {
		log.Printf("handshake failed with status %d, %+v\n", resp.StatusCode, resp)
		io.Copy(os.Stdout, resp.Body)
		resp.Body.Close()
		log.Fatalf("failed")
	} else if err != nil {
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

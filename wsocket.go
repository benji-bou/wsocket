package wsocket

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var (
	ErrSignalChannelClosed = errors.New("client signal is closed")
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

type Communicator interface {
	SendMessage(message interface{}) error
}

type Socket struct {
	write      chan interface{}
	read       chan []byte
	errc       chan error
	closeState bool

	co *websocket.Conn
}

func (c *Socket) Close() {
	log.Println("Closing WebSocket")
	c.co.Close()
	c.closeState = true
	close(c.errc)
	close(c.write)
	close(c.read)
}

func (c *Socket) GetRead() <-chan []byte {
	return c.read
}

func (c *Socket) GetError() <-chan error {
	return c.errc
}

func (c *Socket) GetWrite() chan<- interface{} {
	return c.write
}

func (c *Socket) SendMessage(message interface{}) {
	c.GetWrite() <- message
}

func concurrentRead(cl *Socket) {
	for {
		// event := reflect.New(cl.incomingType).Interface()
		event := json.RawMessage{}
		err := cl.co.ReadJSON(&event)
		if err != nil {
			cl.Close()
			return
		} else {
			// log.Println("Socket received data", string(event))
			cl.read <- event
		}
	}
}

func concurentWrite(cl *Socket) {
	for {
		select {
		case json, ok := <-cl.write:
			if ok == true {
				if err := cl.co.WriteJSON(json); err != nil {
					if cl.closeState == false {
						cl.errc <- err
						return
					}
				}
			} else {
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
	cl := &Socket{co: conn, write: make(chan interface{}), read: make(chan []byte), errc: make(chan error), closeState: false}
	go concurentWrite(cl)
	go concurrentRead(cl)
	return cl, nil
}

func AcceptNewSocket(w http.ResponseWriter, r *http.Request) (*Socket, error) {
	conn, err := initSocket(w, r)
	if err != nil {
		return nil, err
	}

	cl := &Socket{co: conn, write: make(chan interface{}), read: make(chan []byte), errc: make(chan error), closeState: false}
	go concurentWrite(cl)
	go concurrentRead(cl)
	return cl, nil
}

func initSocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return upgrader.Upgrade(w, r, nil)
}

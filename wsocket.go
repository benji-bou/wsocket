package wsocket

import (
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
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

type Client struct {
	write      chan interface{}
	read       chan []byte
	errc       chan error
	closeState bool

	co *websocket.Conn
}

func (c *Client) Close() {
	log.Println("Closing WebSocket")
	c.co.Close()
	c.closeState = true
	close(c.errc)
	close(c.write)
	close(c.read)
}

func (c *Client) GetRead() <-chan []byte {
	return c.read
}

func (c *Client) GetError() <-chan error {
	return c.errc
}

func (c *Client) GetWrite() chan<- interface{} {
	return c.write
}

func (c *Client) SendMessage(message interface{}) {
	c.GetWrite() <- message
}

func concurrentRead(cl *Client) {
	for {
		// event := reflect.New(cl.incomingType).Interface()
		event := json.RawMessage{}
		err := cl.co.ReadJSON(&event)
		log.Println("Received WebSocket Read")
		if err != nil {
			if cl.closeState == false {
				cl.errc <- err
			}
		} else {
			if cl.closeState == false {
				log.Println("send data to read channel if client")
				cl.read <- event
			}
		}
	}
}

func concurentWrite(cl *Client) {
	for {
		select {
		case json, ok := <-cl.write:
			if ok == true {
				if err := cl.co.WriteJSON(json); err != nil {
					if cl.closeState == false {
						cl.errc <- err
					}
				}
			} else {
				return
			}
		}
	}
}

func NewClient(w http.ResponseWriter, r *http.Request) (*Client, error) {
	conn, err := initSocket(w, r)
	if err != nil {
		return nil, err
	}

	cl := &Client{co: conn, write: make(chan interface{}), read: make(chan []byte), errc: make(chan error), closeState: false}
	go concurentWrite(cl)
	go concurrentRead(cl)
	return cl, nil
}

func initSocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return upgrader.Upgrade(w, r, nil)
}

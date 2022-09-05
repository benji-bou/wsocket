package wsocket

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 5 * time.Second

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

type ChanDispatcher struct {
	src chan interface{}
	dst []chan<- interface{}
	add chan chan<- interface{}
}

func NewChanDispatcher() (chan<- interface{}, *ChanDispatcher) {
	source := make(chan interface{})
	cd := &ChanDispatcher{src: source, dst: make([]chan<- interface{}, 0), add: make(chan chan<- interface{})}
	go cd.run()
	return source, cd
}

func (cd *ChanDispatcher) GetNewSubscriber() <-chan interface{} {
	sub := make(chan interface{})
	cd.add <- sub
	return sub
}

func (cd *ChanDispatcher) closeAll() {
	for _, d := range cd.dst {
		close(d)
	}
	close(cd.add)
	cd.dst = nil
}

func (cd *ChanDispatcher) run() {
	for {
		select {
		case data, ok := <-cd.src:
			if !ok {
				cd.closeAll()
				return
			}
			for _, d := range cd.dst {
				d <- data
			}
		case newDst := <-cd.add:
			cd.dst = append(cd.dst, newDst)
		}
	}
}

type Socket struct {
	twrite chan []byte
	read   chan []byte
	bwrite chan []byte
	errc   chan error

	activityDispatcher *ChanDispatcher
	activitySrc        chan<- interface{}
	closingDispatcher  *ChanDispatcher
	closingSrc         chan<- interface{}

	Conn *websocket.Conn
}

func (c *Socket) handleClose() {
	<-c.closingDispatcher.GetNewSubscriber()
	log.Printf("Closing WebSocket")
	c.Conn.Close()
}

func (c *Socket) Read() <-chan []byte {
	return c.read
}

func (c *Socket) Error() <-chan error {
	return c.errc
}

func (c *Socket) Write(messageType int, data []byte) error {
	switch messageType {
	case websocket.BinaryMessage:
		select {
		case c.bwrite <- data:
		default:
			return fmt.Errorf("unable to write bin data")
		}
	case websocket.TextMessage:
		select {
		case c.twrite <- data:
		default:
			return fmt.Errorf("unable to write text data")
		}
	default:
		return fmt.Errorf("unhandle message type to write")
	}
	return nil
}

func (c *Socket) SendMessage(i interface{}) error {
	switch mes := i.(type) {
	case []byte:
		return c.Write(websocket.BinaryMessage, mes)
	case string:
		return c.Write(websocket.TextMessage, []byte(mes))
	case encoding.BinaryMarshaler:
		data, err := mes.MarshalBinary()
		if err != nil {
			return fmt.Errorf("failed to marshal into binary with err : %w", err)
		}
		return c.Write(websocket.BinaryMessage, data)
	case encoding.TextMarshaler:
		data, err := mes.MarshalText()
		if err != nil {
			return fmt.Errorf("failed to marshal into text with err : %w", err)
		}
		return c.Write(websocket.TextMessage, data)
	default:
		data, err := json.Marshal(i)
		if err != nil {
			return fmt.Errorf("failed to marshal into json with err : %w", err)
		}
		return c.Write(websocket.TextMessage, data)
	}
}

func (c *Socket) concurrentRead() {

	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(appData string) error {
		// log.Printf("Receive pong %v\n", appData)
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		c.activitySrc <- struct{}{}
		return nil
	})

	defer func() {
		c.closingSrc <- struct{}{}
		close(c.closingSrc)
	}()
	for {
		_, b, err := c.Conn.ReadMessage()
		if err != nil {
			c.dispatchError(err)
			return
		}
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		c.activitySrc <- struct{}{}
		c.read <- b
	}
}

func (c *Socket) concurentWrite() {
	resetPing := func(nextPing *time.Timer) {
		if !nextPing.Stop() {
			select {
			case <-nextPing.C:
			default:
			}
		}
		nextPing.Reset(pingPeriod)
	}
	nextPing := time.NewTimer(pingPeriod)
	close := c.closingDispatcher.GetNewSubscriber()
	activity := c.activityDispatcher.GetNewSubscriber()
	for {
		select {
		case b := <-c.bwrite:
			if err := c.writeData(websocket.BinaryMessage, b, c.bwrite); err != nil {
				c.dispatchError(err)
				return
			}
		case t := <-c.twrite:
			if err := c.writeData(websocket.TextMessage, t, c.twrite); err != nil {
				c.dispatchError(err)
				return
			}
		case <-close:
			log.Printf("receive close dispatch in write")
			return
		case <-activity:
			resetPing(nextPing)
		case <-nextPing.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			// log.Printf("Write ping")
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.dispatchError(err)
				return
			}
			resetPing(nextPing)
		}
	}
}

func (c *Socket) writeData(messageType int, data []byte, nextData <-chan []byte) error {
	writer, err := c.Conn.NextWriter(messageType)
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	if err != nil {
		return err
	}
	n := len(nextData)
	for i := 0; i < n; i++ {
		writer.Write(<-nextData)
	}
	err = writer.Close()
	if err != nil {
		return err
	}
	return nil
}

func (c *Socket) dispatchError(err error) {
	log.Printf("Dispatch error: %v\n", err)
	select {
	case c.errc <- err:
	default:
		log.Printf("unable to dispatch error on error chan %v\n", err)
	}
}

func NewSocket(conn *websocket.Conn) *Socket {
	closingSrc, closingDispatcher := NewChanDispatcher()
	activitySrc, activityDispatcher := NewChanDispatcher()
	cl := &Socket{Conn: conn,
		closingSrc:         closingSrc,
		closingDispatcher:  closingDispatcher,
		activitySrc:        activitySrc,
		activityDispatcher: activityDispatcher,
		twrite:             make(chan []byte, 256),
		read:               make(chan []byte, 256),
		bwrite:             make(chan []byte, 256),
		errc:               make(chan error)}
	go cl.concurentWrite()
	go cl.handleClose()
	go cl.concurrentRead()
	return cl
}
func ConnectSocket(addr string, header http.Header) (*Socket, error) {
	conn, resp, err := websocket.DefaultDialer.Dial(addr, header)
	if err == websocket.ErrBadHandshake {
		w := &strings.Builder{}
		io.Copy(w, resp.Body)
		resp.Body.Close()
		return nil, errors.New(w.String())
	} else if err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return NewSocket(conn), nil
}

func AcceptNewSocket(w http.ResponseWriter, r *http.Request) (*Socket, error) {
	conn, err := initSocket(w, r)
	if err != nil {
		return nil, err
	}

	return NewSocket(conn), nil
}

func initSocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return upgrader.Upgrade(w, r, nil)
}

package wskit

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func Broadcaster[T any](input <-chan T, timeout time.Duration) http.Handler {
	b := &broadcaster[T]{
		input:   input,
		timeout: timeout,
		clients: make(map[chan<- []byte]bool),
		reg:     make(chan chan<- []byte),
		unreg:   make(chan chan<- []byte),
	}
	go b.run()
	return b
}

type broadcaster[T any] struct {
	input   <-chan T
	timeout time.Duration
	clients map[chan<- []byte]bool
	reg     chan chan<- []byte
	unreg   chan chan<- []byte
}

func (b *broadcaster[T]) run() {
	defer b.close()
	for {
		select {
		case m, ok := <-b.input:
			if !ok {
				return
			}
			b.broadcast(m)

		case client := <-b.reg:
			b.clients[client] = true

		case client := <-b.unreg:
			if _, ok := b.clients[client]; ok {
				delete(b.clients, client)
				close(client)
			}
		}
	}
}

func (b *broadcaster[T]) send(client chan<- []byte, m []byte, wg *sync.WaitGroup) {
	defer wg.Done()
	select {
	case client <- m:
	case <-time.After(b.timeout):
		go func() { b.unreg <- client }()
	}
}

func (b *broadcaster[T]) broadcast(m T) {
	bytes, err := json.Marshal(m)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(len(b.clients))
	for client := range b.clients {
		go b.send(client, bytes, &wg)
	}
	wg.Wait()
}

func (b *broadcaster[T]) close() {
	close(b.reg)
	close(b.unreg)
	for client := range b.clients {
		close(client)
	}
	clear(b.clients)
}

func (b *broadcaster[T]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Could not upgrade to WebSocket", http.StatusBadRequest)
		return
	}
	defer conn.Close()

	msgchan := make(chan []byte)
	b.reg <- msgchan
	defer func() {
		b.unreg <- msgchan
	}()

	for m := range msgchan {
		if err := conn.WriteMessage(websocket.TextMessage, m); err != nil {
			return
		}
	}
}

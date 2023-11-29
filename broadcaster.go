package wskit

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type BroadcasterOption func(*broadcasterOptions)

func WithTimeout(timeout time.Duration) BroadcasterOption {
	return func(bo *broadcasterOptions) {
		bo.timeout = timeout
	}
}

func WithUpgrader(upgrader *websocket.Upgrader) BroadcasterOption {
	return func(bo *broadcasterOptions) {
		bo.upgrader = upgrader
	}
}

func WithResponseHeader(responseHeader http.Header) BroadcasterOption {
	return func(bo *broadcasterOptions) {
		bo.responseHeader = responseHeader
	}
}

func Broadcaster[T any](input <-chan T, options ...BroadcasterOption) http.Handler {
	b := &broadcaster[T]{
		broadcasterOptions: broadcasterOptions{
			upgrader: &defaultUpgrader,
			timeout:  -1,
		},
		input:   input,
		clients: make(map[chan<- []byte]bool),
		reg:     make(chan chan<- []byte),
		unreg:   make(chan chan<- []byte),
	}
	for _, opt := range options {
		opt(&b.broadcasterOptions)
	}
	go b.run()
	return b
}

var defaultUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type broadcaster[T any] struct {
	broadcasterOptions
	input   <-chan T
	clients map[chan<- []byte]bool
	reg     chan chan<- []byte
	unreg   chan chan<- []byte
}

type broadcasterOptions struct {
	timeout        time.Duration
	upgrader       *websocket.Upgrader
	responseHeader http.Header
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

	if b.timeout < 0 {
		for client := range b.clients {
			client <- bytes
		}
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
	conn, err := b.upgrader.Upgrade(w, r, b.responseHeader)
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

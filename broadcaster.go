package wskit

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func Broadcaster[T any](input <-chan T, options ...BroadcasterOption) http.Handler {
	b := &broadcaster[T]{
		broadcasterOptions: defaultBroadcasterOptions,
		input:              input,
		clients:            make(map[chan<- []byte]bool),
		reg:                make(chan chan<- []byte),
		unreg:              make(chan chan<- []byte),
		closed:             make(chan struct{}),
	}
	for _, opt := range options {
		opt(&b.broadcasterOptions)
	}
	go b.run()
	return b
}

type broadcaster[T any] struct {
	broadcasterOptions
	input   <-chan T
	clients map[chan<- []byte]bool
	reg     chan chan<- []byte
	unreg   chan chan<- []byte
	closed  chan struct{}
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

func (b *broadcaster[T]) broadcast(m T) {
	if len(b.clients) == 0 {
		b.logger.Debug("dropping message", slog.Any("message", m))
		return
	}

	bytes, err := b.marshaler(m)
	if err != nil {
		b.logger.Error("failed to serialize broadcasted message",
			slog.Any("message", m),
			slog.Any("err", err))
		return
	}

	var wg sync.WaitGroup
	wg.Add(len(b.clients))

	if b.timeout < 0 {
		for client := range b.clients {
			client := client
			go func() {
				defer wg.Done()
				client <- bytes
			}()
		}
		wg.Wait()
		return
	}

	timeout := make(chan struct{})
	time.AfterFunc(b.timeout, func() { close(timeout) })

	unreg := make(chan chan<- []byte, len(b.clients))
	for client := range b.clients {
		client := client
		go func() {
			defer wg.Done()
			select { // try non-blocking first
			case client <- bytes:
				return
			default:
			}
			select {
			case client <- bytes:
			case <-timeout:
				unreg <- client
			}
		}()
	}
	wg.Wait()
	close(unreg)
	if timeouts := len(unreg); timeouts > 0 {
		b.logger.Debug("timeout", slog.Int("num_clients", timeouts))
		for client := range unreg {
			delete(b.clients, client)
			close(client)
		}
	}
}

func (b *broadcaster[T]) close() {
	close(b.closed)
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
	select {
	case <-b.closed:
		msg := websocket.FormatCloseMessage(http.StatusGone, "Broadcaster is closed")
		conn.WriteControl(websocket.CloseMessage, msg, time.Now())
		return
	case b.reg <- msgchan:
	}
	defer func() {
		select {
		case <-b.closed:
		case b.unreg <- msgchan:
		}
	}()

	b.logger.Debug("connection opened", slog.Any("remote_addr", conn.RemoteAddr()))

	for m := range msgchan {
		if err := conn.WriteMessage(b.messageType, m); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				b.logger.Error("unexpected websocket error",
					slog.Any("err", err),
					slog.Any("remote_addr", conn.RemoteAddr()))
			}
			return
		}
	}

	b.logger.Debug("connection closed", slog.Any("remote_addr", conn.RemoteAddr()))
}

package wskit

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	defaultUpgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	defaultBroadcasterOptions = broadcasterOptions{
		upgrader:    &defaultUpgrader,
		timeout:     -1,
		logger:      slog.Default(),
		marshaler:   json.Marshal,
		messageType: websocket.TextMessage,
	}
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

func WithLogger(logger *slog.Logger) BroadcasterOption {
	return func(bo *broadcasterOptions) {
		bo.logger = logger
	}
}

func WithMarshaler(marshaler func(any) ([]byte, error), isText bool) BroadcasterOption {
	return func(bo *broadcasterOptions) {
		bo.marshaler = marshaler
		if isText {
			bo.messageType = websocket.TextMessage
		} else {
			bo.messageType = websocket.BinaryMessage
		}
	}
}

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

type broadcasterOptions struct {
	timeout        time.Duration
	upgrader       *websocket.Upgrader
	responseHeader http.Header
	logger         *slog.Logger
	marshaler      func(any) ([]byte, error)
	messageType    int
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
		return
	}

	bytes, err := b.marshaler(m)
	if err != nil {
		b.logger.Error("failed to serialize broadcasted message", slog.Any("err", err))
		return
	}

	if b.timeout < 0 {
		for client := range b.clients {
			client <- bytes
		}
		return
	}

	timeout := make(chan struct{})
	time.AfterFunc(b.timeout, func() { close(timeout) })

	unreg := make(chan chan<- []byte, len(b.clients))
	var wg sync.WaitGroup
	wg.Add(len(b.clients))
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
	for client := range unreg {
		delete(b.clients, client)
		close(client)
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

	for m := range msgchan {
		if err := conn.WriteMessage(b.messageType, m); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				b.logger.Error("unexpected websocket error", err, slog.Any("err", err))
			}
			return
		}
	}
}

package wskit

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func Broadcaster[T any](input <-chan T, timeout time.Duration) http.Handler {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	clients := make(map[chan<- []byte]bool)
	reg := make(chan chan<- []byte)
	unreg := make(chan chan<- []byte)

	send := func(client chan<- []byte, m []byte, wg *sync.WaitGroup) {
		defer wg.Done()
		select {
		case client <- m:
		case <-time.After(timeout):
			go func() { unreg <- client }()
		}
	}

	broadcast := func(m T) {
		bytes, err := json.Marshal(m)
		if err != nil {
			return
		}
		var wg sync.WaitGroup
		wg.Add(len(clients))
		for client := range clients {
			go send(client, bytes, &wg)
		}
		wg.Wait()
	}

	go func() {
		defer func() {
			close(reg)
			close(unreg)
			for client := range clients {
				close(client)
			}
			clear(clients)
		}()

		for {
			select {
			case m, ok := <-input:
				if !ok {
					return
				}
				broadcast(m)

			case client := <-reg:
				clients[client] = true

			case client := <-unreg:
				close(client)
				delete(clients, client)
			}
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "Could not upgrade to WebSocket", http.StatusBadRequest)
			return
		}
		defer conn.Close()

		msgchan := make(chan []byte)
		reg <- msgchan
		defer func() {
			unreg <- msgchan
		}()

		for m := range msgchan {
			if err := conn.WriteMessage(websocket.TextMessage, m); err != nil {
				return
			}
		}
	})
}

package wskit

import (
	"encoding/json"
	"log/slog"
	"net/http"
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

type broadcasterOptions struct {
	timeout        time.Duration
	upgrader       *websocket.Upgrader
	responseHeader http.Header
	logger         *slog.Logger
	marshaler      func(any) ([]byte, error)
	messageType    int
}

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

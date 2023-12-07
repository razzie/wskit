package wskit

import (
	"bytes"
	"encoding/gob"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

type testData struct {
	N int
}

type delayedBufferPool struct {
	delay time.Duration
}

func (dbp delayedBufferPool) Get() any {
	time.Sleep(dbp.delay)
	return nil
}

func (delayedBufferPool) Put(any) {
}

func TestBroadcast(t *testing.T) {
	const numMessages = 5
	ch := make(chan testData, numMessages)
	defer close(ch)

	b := Broadcaster(ch)
	server := httptest.NewServer(b)
	defer server.Close()

	url := "ws" + server.URL[4:]

	ws1, _, err := websocket.DefaultDialer.Dial(url, nil)
	assert.NoError(t, err)
	defer ws1.Close()

	ws2, _, err := websocket.DefaultDialer.Dial(url, nil)
	assert.NoError(t, err)
	defer ws2.Close()

	for i := 1; i <= numMessages; i++ {
		ch <- testData{N: i}
	}

	for i := 1; i <= numMessages; i++ {
		var result1, result2 testData
		assert.NoError(t, ws1.ReadJSON(&result1))
		assert.Equal(t, i, result1.N)
		assert.NoError(t, ws2.ReadJSON(&result2))
		assert.Equal(t, i, result2.N)
	}
}

func TestTimeout(t *testing.T) {
	ch := make(chan testData, 2)
	defer close(ch)

	upgrader := &websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		WriteBufferPool: &delayedBufferPool{delay: 200 * time.Millisecond},
	}

	b := Broadcaster(ch, WithTimeout(100*time.Millisecond), WithUpgrader(upgrader))
	server := httptest.NewServer(b)
	defer server.Close()

	url := "ws" + server.URL[4:]
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	assert.NoError(t, err)
	defer ws.Close()

	ch <- testData{N: 1}
	ch <- testData{N: 2}

	var result testData
	assert.NoError(t, ws.ReadJSON(&result))
	assert.NotNil(t, ws.ReadJSON(&result))
}

func encode(o any) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(o); err != nil {
		return nil, err
	}
	return io.ReadAll(&buf)
}

func decode(p []byte, o any) error {
	buf := bytes.NewBuffer(p)
	dec := gob.NewDecoder(buf)
	return dec.Decode(o)
}

func TestCustomMarshaler(t *testing.T) {
	ch := make(chan testData, 1)
	defer close(ch)

	b := Broadcaster(ch, WithMarshaler(encode, false))
	server := httptest.NewServer(b)
	defer server.Close()

	url := "ws" + server.URL[4:]
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	assert.NoError(t, err)
	defer ws.Close()

	ch <- testData{N: 1}

	var m testData
	mType, p, err := ws.ReadMessage()
	assert.NoError(t, err)
	assert.NoError(t, decode(p, &m))
	assert.Equal(t, 1, m.N)
	assert.Equal(t, websocket.BinaryMessage, mType)
}

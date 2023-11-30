package wskit

import (
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

type testData struct {
	N int
}

func TestBroadcast(t *testing.T) {
	ch := make(chan testData)
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

	go func() {
		for i := 1; i <= 5; i++ {
			ch <- testData{N: i}
		}
	}()

	for i := 1; i <= 5; i++ {
		var result1, result2 testData
		assert.NoError(t, ws1.ReadJSON(&result1))
		assert.Equal(t, i, result1.N)
		assert.NoError(t, ws2.ReadJSON(&result2))
		assert.Equal(t, i, result2.N)
	}
}

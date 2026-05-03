package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"easy_terminal/internal/session"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type wsBridge struct {
	rt   *session.RuntimeSession
	conn *websocket.Conn
}

type clientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func serveWS(w http.ResponseWriter, r *http.Request, rt *session.RuntimeSession) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	b := &wsBridge{rt: rt, conn: conn}
	_ = conn.WriteMessage(websocket.BinaryMessage, rt.OutputSnapshot())
	ch, cancel := rt.Subscribe()
	defer cancel()
	defer conn.Close()
	go b.readClient()
	for out := range ch {
		if err := conn.WriteMessage(websocket.BinaryMessage, out); err != nil {
			return
		}
	}
}

func (b *wsBridge) readClient() {
	for {
		_, data, err := b.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg clientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			filtered := filterTerminalResponses([]byte(msg.Data))
			if len(filtered) > 0 {
				_ = b.rt.WriteInput(string(filtered))
			}
		case "resize":
			_ = b.rt.Resize(msg.Cols, msg.Rows)
		case "snapshot":
			b.rt.SetVisibleSnapshot(msg.Data)
		}
	}
}

func filterTerminalResponses(data []byte) []byte {
	data = regexpReplace(data, []byte{0x1b, '['}, 'R')
	data = regexpReplace(data, []byte{0x1b, '[', '?'}, 'c')
	data = filterOSC(data)
	return data
}

func regexpReplace(data []byte, prefix []byte, final byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(data); {
		if bytes.HasPrefix(data[i:], prefix) {
			j := i + len(prefix)
			for j < len(data) && ((data[j] >= '0' && data[j] <= '9') || data[j] == ';') {
				j++
			}
			if j < len(data) && data[j] == final {
				i = j + 1
				continue
			}
		}
		out.WriteByte(data[i])
		i++
	}
	return out.Bytes()
}

func filterOSC(data []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(data); {
		if i+1 < len(data) && data[i] == 0x1b && data[i+1] == ']' {
			j := i + 2
			for j < len(data) {
				if data[j] == 0x07 {
					i = j + 1
					goto next
				}
				if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
					i = j + 2
					goto next
				}
				j++
			}
		}
		out.WriteByte(data[i])
		i++
	next:
	}
	return out.Bytes()
}

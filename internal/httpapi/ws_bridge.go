package httpapi

import (
	"encoding/json"
	"net/http"

	"easy_terminal/internal/session"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type wsBridge struct {
	rt       *session.RuntimeSession
	conn     *websocket.Conn
	headless bool
}

type clientMessage struct {
	Type   string `json:"type"`
	Data   string `json:"data,omitempty"`
	Source string `json:"source,omitempty"`
	Cols   uint16 `json:"cols,omitempty"`
	Rows   uint16 `json:"rows,omitempty"`
}

func serveWS(w http.ResponseWriter, r *http.Request, rt *session.RuntimeSession) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	headless := r.URL.Query().Get("headless") == "1"
	b := &wsBridge{rt: rt, conn: conn, headless: headless}
	_ = conn.WriteMessage(websocket.BinaryMessage, rt.OutputSnapshot())
	ch, cancel := rt.SubscribeWithMode(headless)
	defer cancel()
	defer conn.Close()
	go b.readClient()
	for ev := range ch {
		switch ev.Type {
		case session.RuntimeEventSnapshotRequest:
			msg, _ := json.Marshal(map[string]string{"type": "snapshot_request"})
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		default:
			if err := conn.WriteMessage(websocket.BinaryMessage, ev.Data); err != nil {
				return
			}
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
				b.rt.SetNotificationMentionOpenID("")
				_ = b.rt.WriteInput(string(filtered))
			}
		case "submit":
			_ = session.SubmitStructuredInput(b.rt, msg.Data)
		case "resize":
			_ = b.rt.Resize(msg.Cols, msg.Rows)
		case "snapshot":
			b.rt.SetVisibleSnapshotWithSource(msg.Data, b.snapshotSource(msg.Source))
		}
	}
}

func (b *wsBridge) snapshotSource(source string) string {
	if b.headless {
		return "headless:" + source
	}
	return "browser:" + source
}

func filterTerminalResponses(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] == 0x1b && i+1 < len(data) {
			switch data[i+1] {
			case '[':
				if end, ok := terminalResponseCSIEnd(data, i+2); ok {
					i = end + 1
					continue
				}
			case ']', 'P', '^', '_':
				if end, ok := stringControlEnd(data, i+2); ok {
					i = end + 1
					continue
				}
			}
		}
		out = append(out, data[i])
		i++
	}
	return out
}

func terminalResponseCSIEnd(data []byte, start int) (int, bool) {
	if start >= len(data) || !isCSIParamByte(data[start]) {
		return 0, false
	}
	for i := start; i < len(data); i++ {
		b := data[i]
		if b >= 0x40 && b <= 0x7e {
			return i, isTerminalResponseCSIFinal(b)
		}
		if !isCSIParamByte(b) && !(b >= 0x20 && b <= 0x2f) {
			return 0, false
		}
	}
	return 0, false
}

func isCSIParamByte(b byte) bool {
	return b >= 0x30 && b <= 0x3f
}

func isTerminalResponseCSIFinal(b byte) bool {
	switch b {
	case 'R', 'c', 'n', 't', 'u':
		return true
	default:
		return false
	}
}

func stringControlEnd(data []byte, start int) (int, bool) {
	for i := start; i < len(data); i++ {
		if data[i] == 0x07 {
			return i, true
		}
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
			return i + 1, true
		}
	}
	return 0, false
}

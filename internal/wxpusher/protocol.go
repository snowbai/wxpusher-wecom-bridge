package wxpusher

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

const (
	MsgTypeHeartUp      = 101
	MsgTypeHeart        = 201
	MsgTypeInit         = 202
	MsgTypeError        = 203
	MsgTypeUpdate       = 204
	MsgTypeNotification = 20001
)

type Message struct {
	Type      int             `json:"msgType"`
	Content   string          `json:"content"`
	QID       string          `json:"qid"`
	PushToken string          `json:"pushToken"`
	Title     string          `json:"title"`
	URL       string          `json:"url"`
	Raw       json.RawMessage `json:"-"`
}

func BuildWebSocketURL(host string, id identity.Identity) string {
	values := "version=" + url.QueryEscape(id.Version) + "&platform=" + url.QueryEscape(id.Platform)
	if id.PushToken != "" {
		values += "&pushToken=" + url.QueryEscape(id.PushToken)
	}
	return "wss://" + host + "/ws?" + values
}

func BuildHTTPHeaders(id identity.Identity) http.Header {
	headers := http.Header{}
	headers.Set("platform", id.Platform)
	headers.Set("version", id.Version)
	headers.Set("deviceToken", id.DeviceToken)
	headers.Set("Content-Type", "application/json;charset=UTF-8")
	return headers
}

func ParseMessage(raw []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return Message{}, err
	}
	msg.Raw = append(json.RawMessage(nil), raw...)
	return msg, nil
}

func HeartbeatPayload() []byte {
	return []byte(`{"msgType":101}`)
}

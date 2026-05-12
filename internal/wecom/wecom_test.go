package wecom

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWithNilHTTPClientUsesTimeout(t *testing.T) {
	client := New("x", nil)

	if client.http == nil {
		t.Fatal("http client is nil")
	}
	if client.http.Timeout <= 0 {
		t.Fatalf("http timeout = %s, want positive timeout", client.http.Timeout)
	}
}

func TestBuildOriginalMarkdown(t *testing.T) {
	payload := BuildOriginalMarkdown("q1", "hello")

	for _, want := range []string{"WxPusher 通知", "q1", "hello"} {
		if !strings.Contains(payload.Content, want) {
			t.Fatalf("content = %q, want to contain %q", payload.Content, want)
		}
	}
}

func TestBuildEnrichedMarkdown(t *testing.T) {
	payload := BuildEnrichedMarkdown("q1", "https://example.test/page", "Page Title", "summary text", "/tmp/screenshot.png")

	for _, want := range []string{
		"WxPusher 链接富化",
		"q1",
		"https://example.test/page",
		"Page Title",
		"summary text",
		"/tmp/screenshot.png",
	} {
		if !strings.Contains(payload.Content, want) {
			t.Fatalf("content = %q, want to contain %q", payload.Content, want)
		}
	}
}

func TestMarkdownPayloadJSONUsesContentKey(t *testing.T) {
	body, err := json.Marshal(struct {
		MsgType  string          `json:"msgtype"`
		Markdown MarkdownPayload `json:"markdown"`
	}{
		MsgType:  "markdown",
		Markdown: MarkdownPayload{Content: "hello"},
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	var request struct {
		Markdown map[string]string `json:"markdown"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if request.Markdown["content"] != "hello" {
		t.Fatalf("markdown.content = %q, want hello; body=%s", request.Markdown["content"], body)
	}
	if _, ok := request.Markdown["Content"]; ok {
		t.Fatalf("body used Content key, want content key: %s", body)
	}
}

func TestSendPostsMarkdownPayload(t *testing.T) {
	var body string
	payload := BuildOriginalMarkdown("q1", "hello")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json;charset=utf-8" {
			t.Fatalf("Content-Type = %q, want application/json;charset=utf-8", got)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	err := New(server.URL, http.DefaultClient).Send(context.Background(), payload)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	var request struct {
		MsgType  string `json:"msgtype"`
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if request.MsgType != "markdown" {
		t.Fatalf("msgtype = %q, want markdown", request.MsgType)
	}
	if request.Markdown.Content != payload.Content {
		t.Fatalf("markdown.content = %q, want %q", request.Markdown.Content, payload.Content)
	}
}

func TestSendImageFilePostsImagePayload(t *testing.T) {
	imageBytes := []byte("fake image bytes")
	imagePath := filepath.Join(t.TempDir(), "screenshot.jpg")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	if err := New(server.URL, server.Client()).SendImageFile(context.Background(), imagePath); err != nil {
		t.Fatalf("SendImageFile returned error: %v", err)
	}

	var request struct {
		MsgType string `json:"msgtype"`
		Image   struct {
			Base64 string `json:"base64"`
			MD5    string `json:"md5"`
		} `json:"image"`
	}
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if request.MsgType != "image" {
		t.Fatalf("msgtype = %q, want image", request.MsgType)
	}
	if request.Image.Base64 != base64.StdEncoding.EncodeToString(imageBytes) {
		t.Fatalf("image base64 = %q, want encoded image bytes", request.Image.Base64)
	}
	sum := md5.Sum(imageBytes)
	if request.Image.MD5 != hex.EncodeToString(sum[:]) {
		t.Fatalf("image md5 = %q, want %q", request.Image.MD5, hex.EncodeToString(sum[:]))
	}
}

func TestSendReturnsErrorForNon2xxStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := New(server.URL, server.Client()).Send(context.Background(), MarkdownPayload{Content: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %q, want status code", err)
	}
}

func TestSendReturnsErrorForWeComErrcode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":40001,"errmsg":"invalid credential"}`))
	}))
	defer server.Close()

	err := New(server.URL, server.Client()).Send(context.Background(), MarkdownPayload{Content: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "40001") {
		t.Fatalf("error = %q, want errcode", err)
	}
}

func TestSendReturnsSanitizedErrorForMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer server.Close()

	webhook := server.URL + "?key=secret-webhook-key"
	err := New(webhook, server.Client()).Send(context.Background(), MarkdownPayload{Content: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "decode wecom response failed") {
		t.Fatalf("error = %q, want decode context", err)
	}
	if strings.Contains(err.Error(), "secret-webhook-key") {
		t.Fatalf("error = %q, must not contain webhook key", err)
	}
}

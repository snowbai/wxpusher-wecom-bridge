package wecom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestSendPostsMarkdownPayload(t *testing.T) {
	var body string
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

	err := New(server.URL, http.DefaultClient).Send(context.Background(), BuildOriginalMarkdown("q1", "hello"))
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if !strings.Contains(body, `"msgtype":"markdown"`) {
		t.Fatalf("body = %q, want markdown msgtype", body)
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

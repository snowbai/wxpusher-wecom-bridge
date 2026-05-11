package wecom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildOriginalMarkdown(t *testing.T) {
	payload := BuildOriginalMarkdown("q1", "hello")

	for _, want := range []string{"WxPusher 通知", "q1", "hello"} {
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

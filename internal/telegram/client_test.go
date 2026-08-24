package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	hc := srv.Client()
	c := newWithHTTPClient("testtoken", "12345", srv.URL, hc)
	return c, srv
}

func TestSendMessage_Success(t *testing.T) {
	var received map[string]string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	if err := c.SendMessage(context.Background(), "hello *world*"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if received["chat_id"] != "12345" {
		t.Errorf("expected chat_id 12345, got %q", received["chat_id"])
	}
	if received["text"] != "hello *world*" {
		t.Errorf("expected text 'hello *world*', got %q", received["text"])
	}
	if received["parse_mode"] != "Markdown" {
		t.Errorf("expected parse_mode Markdown, got %q", received["parse_mode"])
	}
}

func TestSendMessage_APIError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))

	err := c.SendMessage(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestSendMessage_URLContainsToken(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	_ = c.SendMessage(context.Background(), "test")
	if !strings.Contains(gotPath, "testtoken") {
		t.Errorf("expected token in path, got: %s", gotPath)
	}
}

func TestSendMessage_ContextCancelled(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.SendMessage(ctx, "test")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	var count atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	// Send 5 messages well under the 20/min limit
	for i := 0; i < 5; i++ {
		if err := c.SendMessage(context.Background(), "msg"); err != nil {
			t.Fatalf("message %d failed: %v", i, err)
		}
	}
	if count.Load() != 5 {
		t.Errorf("expected 5 requests, got %d", count.Load())
	}
}

func TestConfigured(t *testing.T) {
	tests := []struct {
		token, chatID string
		want          bool
	}{
		{"tok", "id", true},
		{"", "id", false},
		{"tok", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		c := New(tt.token, tt.chatID)
		if got := c.Configured(); got != tt.want {
			t.Errorf("Configured(%q, %q) = %v, want %v", tt.token, tt.chatID, got, tt.want)
		}
	}
}

func TestWaitForRateLimit_RespectsWindow(t *testing.T) {
	c := &Client{
		botToken: "t",
		chatID:   "c",
		apiBase:  "http://unused",
	}

	// Pre-fill with maxPerMinute recent timestamps to trigger rate limiting
	now := time.Now()
	c.sentAt = make([]time.Time, maxPerMinute)
	for i := range c.sentAt {
		c.sentAt[i] = now
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.waitForRateLimit(ctx)
	if err == nil {
		t.Error("expected rate limit to block and context to expire")
	}
}

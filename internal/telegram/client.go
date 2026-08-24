// Package telegram provides a minimal Telegram Bot API client for sending alert messages.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	// maxPerMinute is the Telegram Bot API rate limit for group chats (20 msgs/min).
	maxPerMinute = 20
	apiBase      = "https://api.telegram.org"
)

// Client sends messages to a Telegram chat via the Bot API.
type Client struct {
	botToken   string
	chatID     string
	httpClient *http.Client
	apiBase    string // overridable for tests

	mu       sync.Mutex
	sentAt   []time.Time // timestamps of recent sends for rate limiting
}

// New creates a Telegram client for the given bot token and chat ID.
func New(botToken, chatID string) *Client {
	return &Client{
		botToken:   botToken,
		chatID:     chatID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiBase:    apiBase,
	}
}

// newWithHTTPClient creates a client with a custom HTTP client (for testing).
func newWithHTTPClient(botToken, chatID, base string, hc *http.Client) *Client {
	return &Client{
		botToken:   botToken,
		chatID:     chatID,
		httpClient: hc,
		apiBase:    base,
	}
}

// SendMessage sends a Markdown-formatted message to the configured chat.
// It enforces the rate limit before sending.
func (c *Client) SendMessage(ctx context.Context, text string) error {
	if err := c.waitForRateLimit(ctx); err != nil {
		return err
	}

	payload := map[string]string{
		"chat_id":    c.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", c.apiBase, c.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("telegram: API error %d: %s", resp.StatusCode, errResp.Description)
	}

	c.mu.Lock()
	c.sentAt = append(c.sentAt, time.Now())
	c.mu.Unlock()

	return nil
}

// waitForRateLimit blocks until sending is within the rate limit, or ctx is cancelled.
// Telegram allows 20 messages/minute to a group chat.
func (c *Client) waitForRateLimit(ctx context.Context) error {
	for {
		c.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-time.Minute)

		// Prune timestamps older than 1 minute.
		i := 0
		for i < len(c.sentAt) && c.sentAt[i].Before(cutoff) {
			i++
		}
		c.sentAt = c.sentAt[i:]

		if len(c.sentAt) < maxPerMinute {
			c.mu.Unlock()
			return nil
		}

		// Wait until the oldest send falls outside the window.
		waitUntil := c.sentAt[0].Add(time.Minute)
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(waitUntil)):
		}
	}
}

// Configured returns true if the client has a non-empty bot token and chat ID.
func (c *Client) Configured() bool {
	return c.botToken != "" && c.chatID != ""
}

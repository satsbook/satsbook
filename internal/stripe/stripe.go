package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client makes raw HTTP calls to the Stripe API.
type Client struct {
	SecretKey  string
	HTTPClient *http.Client
}

// CheckoutParams holds the parameters for creating a Checkout Session.
type CheckoutParams struct {
	PriceID    string
	SuccessURL string
	CancelURL  string
	Tier       string // stored as metadata
}

// CheckoutSession is the response from Stripe's create session endpoint.
type CheckoutSession struct {
	ID                 string `json:"id"`
	URL                string `json:"url"`
	PaymentStatus      string `json:"payment_status"`
	CustomerEmail      string `json:"customer_email"`
	CustomerID         string `json:"customer"`
	SubscriptionID     string `json:"subscription"`
	Metadata           map[string]string `json:"metadata"`
	CustomerDetailsRaw json.RawMessage   `json:"customer_details"`
}

// CustomerDetails returns the email from customer_details if present.
func (cs *CheckoutSession) Email() string {
	if cs.CustomerEmail != "" {
		return cs.CustomerEmail
	}
	var details struct {
		Email string `json:"email"`
	}
	if len(cs.CustomerDetailsRaw) > 0 {
		json.Unmarshal(cs.CustomerDetailsRaw, &details)
	}
	return details.Email
}

// CreateCheckoutSession creates a Stripe Checkout Session.
func (c *Client) CreateCheckoutSession(params CheckoutParams) (*CheckoutSession, error) {
	form := url.Values{
		"mode":                            {"subscription"},
		"line_items[0][price]":            {params.PriceID},
		"line_items[0][quantity]":         {"1"},
		"success_url":                     {params.SuccessURL},
		"cancel_url":                      {params.CancelURL},
		"metadata[tier]":                  {params.Tier},
		"subscription_data[metadata][tier]": {params.Tier},
	}

	var session CheckoutSession
	if err := c.post("/v1/checkout/sessions", form, &session); err != nil {
		return nil, fmt.Errorf("create checkout session: %w", err)
	}
	return &session, nil
}

// GetCheckoutSession retrieves a Checkout Session by ID.
func (c *Client) GetCheckoutSession(sessionID string) (*CheckoutSession, error) {
	var session CheckoutSession
	if err := c.get("/v1/checkout/sessions/"+sessionID, &session); err != nil {
		return nil, fmt.Errorf("get checkout session: %w", err)
	}
	return &session, nil
}

// Subscription represents a Stripe subscription object (partial).
type Subscription struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Customer string            `json:"customer"`
	Metadata map[string]string `json:"metadata"`
	Items    struct {
		Data []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

// GetSubscription retrieves a subscription by ID.
func (c *Client) GetSubscription(subID string) (*Subscription, error) {
	var sub Subscription
	if err := c.get("/v1/subscriptions/"+subID, &sub); err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return &sub, nil
}

func (c *Client) post(path string, form url.Values, result any) error {
	req, err := http.NewRequest(http.MethodPost, "https://api.stripe.com"+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.SecretKey, "")
	return c.do(req, result)
}

func (c *Client) get(path string, result any) error {
	req, err := http.NewRequest(http.MethodGet, "https://api.stripe.com"+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.SecretKey, "")
	return c.do(req, result)
}

func (c *Client) do(req *http.Request, result any) error {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("stripe API error (status %d): %s", resp.StatusCode, body)
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// WebhookEvent represents a Stripe webhook event.
type WebhookEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ObjectRaw returns the data.object field as raw JSON.
func (e *WebhookEvent) ObjectRaw() json.RawMessage {
	var wrapper struct {
		Object json.RawMessage `json:"object"`
	}
	json.Unmarshal(e.Data, &wrapper)
	return wrapper.Object
}

// VerifyWebhookSignature verifies a Stripe webhook signature.
// Returns the parsed event if valid.
func VerifyWebhookSignature(payload []byte, sigHeader, secret string) (*WebhookEvent, error) {
	// Parse the Stripe-Signature header: t=timestamp,v1=signature
	parts := strings.Split(sigHeader, ",")
	var timestamp string
	var signatures []string
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			signatures = append(signatures, kv[1])
		}
	}

	if timestamp == "" || len(signatures) == 0 {
		return nil, fmt.Errorf("invalid signature header")
	}

	// Check timestamp is within tolerance (5 minutes).
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}
	if abs(time.Now().Unix()-ts) > 300 {
		return nil, fmt.Errorf("timestamp too old")
	}

	// Compute expected signature: HMAC-SHA256(secret, "timestamp.payload")
	signed := timestamp + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	expected := hex.EncodeToString(mac.Sum(nil))

	for _, sig := range signatures {
		if hmac.Equal([]byte(sig), []byte(expected)) {
			var event WebhookEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				return nil, fmt.Errorf("decode event: %w", err)
			}
			return &event, nil
		}
	}

	return nil, fmt.Errorf("signature mismatch")
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

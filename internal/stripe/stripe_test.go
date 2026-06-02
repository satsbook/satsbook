package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifyWebhookSignature_Valid(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"id":"evt_123","type":"checkout.session.completed","data":{"object":{"id":"cs_123"}}}`)
	ts := fmt.Sprintf("%d", time.Now().Unix())

	sig := computeSignature(ts, payload, secret)
	header := fmt.Sprintf("t=%s,v1=%s", ts, sig)

	event, err := VerifyWebhookSignature(payload, header, secret)
	if err != nil {
		t.Fatalf("expected valid signature: %v", err)
	}
	if event.ID != "evt_123" {
		t.Errorf("event ID: got %q", event.ID)
	}
	if event.Type != "checkout.session.completed" {
		t.Errorf("event type: got %q", event.Type)
	}
}

func TestVerifyWebhookSignature_Invalid(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"id":"evt_123","type":"test"}`)
	ts := fmt.Sprintf("%d", time.Now().Unix())

	header := fmt.Sprintf("t=%s,v1=%s", ts, "badsignature")

	_, err := VerifyWebhookSignature(payload, header, secret)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestVerifyWebhookSignature_Expired(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"id":"evt_123","type":"test"}`)
	ts := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())

	sig := computeSignature(ts, payload, secret)
	header := fmt.Sprintf("t=%s,v1=%s", ts, sig)

	_, err := VerifyWebhookSignature(payload, header, secret)
	if err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

func TestVerifyWebhookSignature_MissingHeader(t *testing.T) {
	_, err := VerifyWebhookSignature([]byte(`{}`), "", "secret")
	if err == nil {
		t.Fatal("expected error for empty header")
	}
}

func TestObjectRaw(t *testing.T) {
	event := &WebhookEvent{
		Data: json.RawMessage(`{"object":{"id":"cs_test","customer":"cus_abc"}}`),
	}
	raw := event.ObjectRaw()

	var obj struct {
		ID       string `json:"id"`
		Customer string `json:"customer"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if obj.ID != "cs_test" {
		t.Errorf("id: got %q", obj.ID)
	}
	if obj.Customer != "cus_abc" {
		t.Errorf("customer: got %q", obj.Customer)
	}
}

func TestCheckoutSessionEmail(t *testing.T) {
	t.Run("from customer_email field", func(t *testing.T) {
		cs := &CheckoutSession{CustomerEmail: "direct@test.com"}
		if cs.Email() != "direct@test.com" {
			t.Errorf("got %q", cs.Email())
		}
	})

	t.Run("from customer_details", func(t *testing.T) {
		cs := &CheckoutSession{
			CustomerDetailsRaw: json.RawMessage(`{"email":"details@test.com"}`),
		}
		if cs.Email() != "details@test.com" {
			t.Errorf("got %q", cs.Email())
		}
	})

	t.Run("empty", func(t *testing.T) {
		cs := &CheckoutSession{}
		if cs.Email() != "" {
			t.Errorf("got %q", cs.Email())
		}
	})
}

func TestCreateCheckoutSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout/sessions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		user, _, ok := r.BasicAuth()
		if !ok || user != "sk_test_key" {
			t.Errorf("bad auth: %q", user)
		}

		r.ParseForm()
		if r.FormValue("mode") != "subscription" {
			t.Errorf("mode: %q", r.FormValue("mode"))
		}
		if r.FormValue("metadata[tier]") != "pro" {
			t.Errorf("metadata[tier]: %q", r.FormValue("metadata[tier]"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CheckoutSession{
			ID:  "cs_test_session",
			URL: "https://checkout.stripe.com/test",
		})
	}))
	defer srv.Close()

	c := &Client{
		SecretKey:  "sk_test_key",
		HTTPClient: srv.Client(),
	}
	// Override the Stripe API URL by using a custom transport
	c.HTTPClient.Transport = rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	session, err := c.CreateCheckoutSession(CheckoutParams{
		PriceID:    "price_test",
		SuccessURL: "https://example.com/success",
		CancelURL:  "https://example.com/cancel",
		Tier:       "pro",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.ID != "cs_test_session" {
		t.Errorf("session ID: %q", session.ID)
	}
}

func TestGetCheckoutSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout/sessions/cs_abc" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CheckoutSession{
			ID:             "cs_abc",
			PaymentStatus:  "paid",
			SubscriptionID: "sub_xyz",
			Metadata:       map[string]string{"tier": "power"},
		})
	}))
	defer srv.Close()

	c := &Client{
		SecretKey:  "sk_test_key",
		HTTPClient: &http.Client{Transport: rewriteTransport{base: srv.Client().Transport, target: srv.URL}},
	}

	session, err := c.GetCheckoutSession("cs_abc")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.PaymentStatus != "paid" {
		t.Errorf("payment status: %q", session.PaymentStatus)
	}
	if session.SubscriptionID != "sub_xyz" {
		t.Errorf("subscription id: %q", session.SubscriptionID)
	}
}

func TestClientAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer srv.Close()

	c := &Client{
		SecretKey:  "sk_test",
		HTTPClient: &http.Client{Transport: rewriteTransport{base: srv.Client().Transport, target: srv.URL}},
	}

	_, err := c.GetCheckoutSession("cs_bad")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

// rewriteTransport redirects requests from api.stripe.com to the test server.
type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func computeSignature(ts string, payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(payload)))
	return hex.EncodeToString(mac.Sum(nil))
}

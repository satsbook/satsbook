package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/satsbook/satsbook/internal/license"
	"github.com/satsbook/satsbook/internal/licensedb"
	stripeutil "github.com/satsbook/satsbook/internal/stripe"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "create":
		cmdCreate(os.Args[2:])
	case "revoke":
		cmdRevoke(os.Args[2:])
	case "upgrade":
		cmdUpgrade(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: licenseserver <command> [flags]

Commands:
  serve     Start the license validation HTTP server
  create    Create a new license key
  revoke    Revoke a license key
  upgrade   Change a license's tier
  list      List all licenses
`)
}

func getDBPath() string {
	p := os.Getenv("SATSBOOK_LICENSE_DB_PATH")
	if p == "" {
		p = "licenses.db"
	}
	return p
}

func openStore() *licensedb.Store {
	s, err := licensedb.Open(getDBPath())
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	return s
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.String("port", "", "listen port (default: $PORT or 8080)")
	fs.Parse(args)

	p := *port
	if p == "" {
		p = os.Getenv("PORT")
	}
	if p == "" {
		p = "8080"
	}

	privKeyHex := os.Getenv("SATSBOOK_LICENSE_SIGNING_KEY")
	if privKeyHex == "" {
		log.Fatal("SATSBOOK_LICENSE_SIGNING_KEY env var is required")
	}
	privBytes, err := hex.DecodeString(privKeyHex)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		log.Fatalf("invalid SATSBOOK_LICENSE_SIGNING_KEY (expected %d hex-encoded bytes)", ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(privBytes)

	store := openStore()
	defer store.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/license/validate", validateHandler(store, priv))

	// Stripe integration (optional — only enabled if STRIPE_SECRET_KEY is set)
	if sc := loadStripeConfig(); sc != nil {
		client := &stripeutil.Client{SecretKey: sc.secretKey}
		mux.HandleFunc("/v1/checkout", checkoutHandler(client, sc))
		mux.HandleFunc("/v1/stripe/webhook", webhookHandler(store, client, sc))
		mux.HandleFunc("/v1/checkout/success", successHandler(store, client))
		log.Printf("stripe integration enabled")
	} else {
		log.Printf("stripe integration disabled (STRIPE_SECRET_KEY not set)")
	}

	addr := ":" + p
	log.Printf("license server listening on %s (db=%s)", addr, getDBPath())
	log.Fatal(http.ListenAndServe(addr, mux))
}

type validateRequest struct {
	Key string `json:"key"`
}

type validateResponse struct {
	Valid bool   `json:"valid"`
	Tier  string `json:"tier"`
	Token string `json:"token"`
}

func validateHandler(store *licensedb.Store, priv ed25519.PrivateKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req validateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(validateResponse{Valid: false})
			return
		}

		lic, err := store.Lookup(req.Key)
		if err != nil {
			log.Printf("lookup error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if !lic.IsValid() {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(validateResponse{Valid: false})
			return
		}

		token, err := license.SignToken(license.TokenPayload{
			Key:       req.Key,
			Tier:      lic.Tier,
			ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
		}, priv)
		if err != nil {
			log.Printf("sign error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(validateResponse{
			Valid: true,
			Tier:  lic.Tier,
			Token: token,
		})
	}
}

func cmdCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	email := fs.String("email", "", "customer email")
	tier := fs.String("tier", "pro", "license tier (free, pro, power)")
	days := fs.Int("days", 0, "days until expiry (0 = no expiry)")
	fs.Parse(args)

	var params *licensedb.CreateParams
	if *days > 0 {
		t := time.Now().Add(time.Duration(*days) * 24 * time.Hour)
		params = &licensedb.CreateParams{ExpiresAt: &t}
	}

	store := openStore()
	defer store.Close()

	lic, err := store.Create(*email, *tier, params)
	if err != nil {
		log.Fatalf("create: %v", err)
	}

	fmt.Printf("Created license:\n")
	fmt.Printf("  Key:     %s\n", lic.LicenseKey)
	fmt.Printf("  Email:   %s\n", lic.Email)
	fmt.Printf("  Tier:    %s\n", lic.Tier)
	if lic.ExpiresAt != nil {
		fmt.Printf("  Expires: %s\n", lic.ExpiresAt.Format(time.RFC3339))
	} else {
		fmt.Printf("  Expires: never\n")
	}
}

func cmdRevoke(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: licenseserver revoke <license_key>\n")
		os.Exit(1)
	}

	store := openStore()
	defer store.Close()

	if err := store.Revoke(args[0]); err != nil {
		log.Fatalf("revoke: %v", err)
	}
	fmt.Printf("Revoked: %s\n", args[0])
}

func cmdUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	tier := fs.String("tier", "", "new tier (pro, power)")
	fs.Parse(args)

	if fs.NArg() < 1 || *tier == "" {
		fmt.Fprintf(os.Stderr, "usage: licenseserver upgrade -tier <tier> <license_key>\n")
		os.Exit(1)
	}

	store := openStore()
	defer store.Close()

	if err := store.Upgrade(fs.Arg(0), *tier); err != nil {
		log.Fatalf("upgrade: %v", err)
	}
	fmt.Printf("Upgraded %s to %s\n", fs.Arg(0), *tier)
}

// --- Stripe integration ---

type stripeConfig struct {
	secretKey     string
	webhookSecret string
	pricePro      string
	pricePower    string
	baseURL       string
}

func loadStripeConfig() *stripeConfig {
	sk := os.Getenv("STRIPE_SECRET_KEY")
	if sk == "" {
		return nil
	}
	return &stripeConfig{
		secretKey:     sk,
		webhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		pricePro:      os.Getenv("STRIPE_PRICE_PRO"),
		pricePower:    os.Getenv("STRIPE_PRICE_POWER"),
		baseURL:       os.Getenv("SATSBOOK_BASE_URL"),
	}
}

func (sc *stripeConfig) priceForTier(tier string) string {
	switch tier {
	case "pro":
		return sc.pricePro
	case "power":
		return sc.pricePower
	default:
		return ""
	}
}

func (sc *stripeConfig) tierForPrice(priceID string) string {
	switch priceID {
	case sc.pricePro:
		return "pro"
	case sc.pricePower:
		return "power"
	default:
		return ""
	}
}

func checkoutHandler(client *stripeutil.Client, sc *stripeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Tier string `json:"tier"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		priceID := sc.priceForTier(req.Tier)
		if priceID == "" {
			http.Error(w, "invalid tier", http.StatusBadRequest)
			return
		}

		session, err := client.CreateCheckoutSession(stripeutil.CheckoutParams{
			PriceID:    priceID,
			SuccessURL: sc.baseURL + "/v1/checkout/success?session_id={CHECKOUT_SESSION_ID}",
			CancelURL:  sc.baseURL + "/v1/checkout/cancel",
			Tier:       req.Tier,
		})
		if err != nil {
			log.Printf("create checkout: %v", err)
			http.Error(w, "failed to create checkout session", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": session.URL})
	}
}

func webhookHandler(store *licensedb.Store, client *stripeutil.Client, sc *stripeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		event, err := stripeutil.VerifyWebhookSignature(body, r.Header.Get("Stripe-Signature"), sc.webhookSecret)
		if err != nil {
			log.Printf("webhook signature error: %v", err)
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}

		switch event.Type {
		case "checkout.session.completed":
			handleCheckoutCompleted(store, client, sc, event)
		case "customer.subscription.deleted":
			handleSubscriptionDeleted(store, event)
		case "customer.subscription.updated":
			handleSubscriptionUpdated(store, client, sc, event)
		default:
			log.Printf("webhook: ignoring event type %s", event.Type)
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handleCheckoutCompleted(store *licensedb.Store, client *stripeutil.Client, sc *stripeConfig, event *stripeutil.WebhookEvent) {
	var session stripeutil.CheckoutSession
	if err := json.Unmarshal(event.ObjectRaw(), &session); err != nil {
		log.Printf("webhook: decode session: %v", err)
		return
	}

	tier := session.Metadata["tier"]
	if tier == "" {
		// Try to determine from subscription
		if session.SubscriptionID != "" {
			sub, err := client.GetSubscription(session.SubscriptionID)
			if err == nil && len(sub.Items.Data) > 0 {
				tier = sc.tierForPrice(sub.Items.Data[0].Price.ID)
			}
			if tier == "" {
				tier = sub.Metadata["tier"]
			}
		}
	}
	if tier == "" {
		tier = "pro" // default
	}

	email := session.Email()
	customerID := session.CustomerID

	// Check if we already created a license for this subscription (idempotent).
	if session.SubscriptionID != "" {
		existing, _ := store.LookupByStripeSubscription(session.SubscriptionID)
		if existing != nil {
			log.Printf("webhook: license already exists for subscription %s", session.SubscriptionID)
			return
		}
	}

	lic, err := store.Create(email, tier, &licensedb.CreateParams{
		StripeCustomerID:     customerID,
		StripeSubscriptionID: session.SubscriptionID,
	})
	if err != nil {
		log.Printf("webhook: create license: %v", err)
		return
	}

	log.Printf("webhook: created license %s (tier=%s, email=%s, sub=%s)", lic.LicenseKey, tier, email, session.SubscriptionID)
}

func handleSubscriptionDeleted(store *licensedb.Store, event *stripeutil.WebhookEvent) {
	var sub struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(event.ObjectRaw(), &sub); err != nil {
		log.Printf("webhook: decode subscription: %v", err)
		return
	}

	lic, err := store.LookupByStripeSubscription(sub.ID)
	if err != nil || lic == nil {
		log.Printf("webhook: no license for subscription %s", sub.ID)
		return
	}

	if err := store.Revoke(lic.LicenseKey); err != nil {
		log.Printf("webhook: revoke license %s: %v", lic.LicenseKey, err)
		return
	}
	log.Printf("webhook: revoked license %s (sub=%s cancelled)", lic.LicenseKey, sub.ID)
}

func handleSubscriptionUpdated(store *licensedb.Store, client *stripeutil.Client, sc *stripeConfig, event *stripeutil.WebhookEvent) {
	var sub stripeutil.Subscription
	if err := json.Unmarshal(event.ObjectRaw(), &sub); err != nil {
		log.Printf("webhook: decode subscription: %v", err)
		return
	}

	lic, err := store.LookupByStripeSubscription(sub.ID)
	if err != nil || lic == nil {
		log.Printf("webhook: no license for subscription %s (update ignored)", sub.ID)
		return
	}

	// Determine new tier from the subscription's current price.
	newTier := ""
	if len(sub.Items.Data) > 0 {
		newTier = sc.tierForPrice(sub.Items.Data[0].Price.ID)
	}
	if newTier == "" {
		newTier = sub.Metadata["tier"]
	}
	if newTier == "" || newTier == lic.Tier {
		return // no tier change
	}

	if err := store.Upgrade(lic.LicenseKey, newTier); err != nil {
		log.Printf("webhook: upgrade license %s to %s: %v", lic.LicenseKey, newTier, err)
		return
	}
	log.Printf("webhook: upgraded license %s from %s to %s", lic.LicenseKey, lic.Tier, newTier)
}

func successHandler(store *licensedb.Store, client *stripeutil.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}

		// Fetch the checkout session from Stripe to get the subscription ID.
		session, err := client.GetCheckoutSession(sessionID)
		if err != nil {
			log.Printf("success: get session %s: %v", sessionID, err)
			http.Error(w, "could not retrieve session", http.StatusInternalServerError)
			return
		}

		if session.PaymentStatus != "paid" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(successPage("", "Payment is still processing. Please check back shortly.")))
			return
		}

		// Look up the license by subscription ID.
		var lic *licensedb.License
		if session.SubscriptionID != "" {
			lic, _ = store.LookupByStripeSubscription(session.SubscriptionID)
		}

		if lic == nil {
			// Webhook may not have arrived yet — show a waiting message.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(successPage("", "Your payment was successful! Your license key is being generated. Please refresh this page in a few seconds.")))
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(successPage(lic.LicenseKey, "")))
	}
}

func successPage(licenseKey, message string) string {
	content := ""
	if licenseKey != "" {
		content = `
		<h2>Thank you for subscribing!</h2>
		<p>Your license key:</p>
		<div style="background:#1a1a2e;border:1px solid #444;border-radius:8px;padding:16px;margin:16px 0;font-family:monospace;font-size:1.2rem;word-break:break-all;user-select:all;">` + licenseKey + `</div>
		<p>Copy this key and set it in your Satsbook settings:</p>
		<pre style="background:#1a1a2e;border:1px solid #444;border-radius:8px;padding:12px;overflow-x:auto;">SATSBOOK_LICENSE_KEY=` + licenseKey + `</pre>
		<p style="color:#999;margin-top:16px;font-size:0.85rem;">Save this key — you will need it if you reinstall Satsbook.</p>`
	} else {
		content = `<h2>` + message + `</h2>`
	}

	return `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Satsbook — License</title>
	<style>
		body { font-family: -apple-system, sans-serif; background: #0d1117; color: #e6e6e6; max-width: 600px; margin: 40px auto; padding: 0 20px; }
		h2 { color: #f7931a; }
		a { color: #4fc3f7; }
	</style>
</head>
<body>` + content + `</body></html>`
}

func cmdList(args []string) {
	store := openStore()
	defer store.Close()

	licenses, err := store.List()
	if err != nil {
		log.Fatalf("list: %v", err)
	}

	if len(licenses) == 0 {
		fmt.Println("No licenses found.")
		return
	}

	fmt.Printf("%-30s %-25s %-8s %-10s %s\n", "KEY", "EMAIL", "TIER", "STATUS", "EXPIRES")
	for _, l := range licenses {
		exp := "never"
		if l.ExpiresAt != nil {
			exp = l.ExpiresAt.Format("2006-01-02")
		}
		fmt.Printf("%-30s %-25s %-8s %-10s %s\n", l.LicenseKey, l.Email, l.Tier, l.Status, exp)
	}
}

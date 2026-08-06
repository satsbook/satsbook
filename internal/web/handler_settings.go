package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/satsbook/satsbook/internal/license"
	"github.com/satsbook/satsbook/internal/monarch"
)

// SettingsPageData holds data for the settings page.
type SettingsPageData struct {
	Tier             string
	LicenseKey       string // masked
	CheckoutURL      string // Stripe checkout URL for subscribing
	MonarchToken     string
	MonarchConnected bool
	MonarchUnlocked  bool
	Message          string
	Error            string
	// Auto-activation from Stripe redirect
	AutoActivateKey string
	// Monarch transaction sync
	MonarchSyncTypes   []string // currently selected tx types
	MonarchSyncSources []string // currently selected sources
	AvailableTxTypes   []string // all tx types from data
	AvailableSources   []string // all sources from data
	MonarchSyncedCount int      // how many transactions have been synced
}

// HandleSettingsPage serves GET /settings.
func (h *Handler) HandleSettingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tier := TierFromContext(ctx)

	// Handle auto-activation from Stripe redirect (?license_key=sk_xxx)
	autoKey := r.URL.Query().Get("license_key")
	if autoKey != "" && h.settingsStore != nil && h.licenseChecker != nil {
		_ = h.settingsStore.SetSetting(ctx, "license_key", autoKey)
		if err := h.licenseChecker.SetKeyAndVerify(ctx, autoKey); err != nil {
			h.logger.Printf("auto-activate license: %v", err)
		} else {
			tier = h.licenseChecker.CurrentTier()
		}
	}

	data := SettingsPageData{
		Tier:            string(tier),
		MonarchUnlocked: license.TierAtLeast(tier, license.TierPower),
		AutoActivateKey: autoKey,
	}

	// Show masked license key
	if h.licenseChecker != nil && h.licenseChecker.LicenseKey() != "" {
		data.LicenseKey = maskToken(h.licenseChecker.LicenseKey())
	}

	if data.MonarchUnlocked && h.settingsStore != nil {
		token, _ := h.settingsStore.GetSetting(r.Context(), "monarch_token")
		if token != "" {
			data.MonarchToken = maskToken(token)
			data.MonarchConnected = true
		}

		// Load tx sync preferences
		syncTypes, _ := h.settingsStore.GetSetting(r.Context(), "monarch_sync_types")
		if syncTypes != "" {
			data.MonarchSyncTypes = strings.Split(syncTypes, ",")
		}
		syncSources, _ := h.settingsStore.GetSetting(r.Context(), "monarch_sync_sources")
		if syncSources != "" {
			data.MonarchSyncSources = strings.Split(syncSources, ",")
		}

		// Load available tx types and sources from data
		if h.monarchTxStore != nil {
			sources, txTypes, _ := h.monarchTxStore.DistinctTransactionValues(r.Context())
			data.AvailableTxTypes = txTypes
			data.AvailableSources = sources
			count, _ := h.monarchTxStore.MonarchSyncedCount(r.Context())
			data.MonarchSyncedCount = count
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "settings_layout", data); err != nil {
		h.logger.Printf("failed to render settings page: %v", err)
	}
}

// PlansPageData holds data for the /settings/plans page.
type PlansPageData struct {
	Tier string
}

// HandlePlansPage serves GET /settings/plans.
func (h *Handler) HandlePlansPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := PlansPageData{
		Tier: string(TierFromContext(ctx)),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "plans_layout", data); err != nil {
		h.logger.Printf("failed to render plans page: %v", err)
	}
}

// HandleMonarchSave handles POST /api/monarch/save — logs into Monarch and stores the auth token.
// Supports two-step flow: if OTP is required, returns an OTP input form.
func (h *Handler) HandleMonarchSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.settingsStore == nil {
		http.Error(w, "settings not available", http.StatusInternalServerError)
		return
	}

	email := strings.TrimSpace(r.FormValue("monarch_email"))
	password := r.FormValue("monarch_password")
	otpCode := strings.TrimSpace(r.FormValue("monarch_otp"))

	if email == "" || password == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Email and password are required.</div>`)
		return
	}

	var syncer *monarch.Syncer
	var token string
	var err error

	if otpCode != "" && h.pendingMonarch != nil {
		// Step 2: complete login with OTP using the same client session
		syncer, token, err = h.pendingMonarch.CompleteOTP(r.Context(), email, password, otpCode)
		h.pendingMonarch = nil
	} else {
		// Step 1: attempt login
		var pending *monarch.PendingClient
		syncer, token, pending, err = monarch.NewSyncerWithLogin(r.Context(), email, password, "")
		if errors.Is(err, monarch.ErrOTPRequired) {
			// Store pending client for step 2
			h.pendingMonarch = pending
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<div class="alert alert-info">Check your email for a verification code.</div>
<form hx-post="/api/monarch/save" hx-target="#monarch-result" style="display: flex; gap: 8px; align-items: flex-end; margin-top: 8px;">
  <input type="hidden" name="monarch_email" value="%s">
  <input type="hidden" name="monarch_password" value="%s">
  <div>
    <label style="font-size: 0.8rem; color: #aaa;">Verification Code</label>
    <input type="text" name="monarch_otp" placeholder="6-digit code" style="width: 160px; margin-top: 4px;" required autofocus>
  </div>
  <button type="submit" class="btn btn-primary">Verify</button>
</form>`, email, password)
			return
		}
	}

	if err != nil {
		h.logger.Printf("monarch login failed: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="alert alert-error">Login failed: %s</div>`, err.Error())
		return
	}

	// Store the token
	if err := h.settingsStore.SetSetting(r.Context(), "monarch_token", token); err != nil {
		h.logger.Printf("failed to save monarch token: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Login succeeded but failed to save token.</div>`)
		return
	}

	// Hot-swap the syncer (no restart needed)
	h.monarchSyncer = syncer

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, monarchConnectedHTML)
}

const monarchConnectedHTML = `<div class="alert alert-success">Connected to Monarch Money.</div>
<div style="display: flex; gap: 8px; margin-top: 12px;">
  <button hx-post="/api/monarch/sync" hx-target="#monarch-result" class="btn btn-primary">Sync Now</button>
  <button hx-post="/api/monarch/disconnect" hx-target="#monarch-result" hx-confirm="Disconnect Monarch Money?" class="btn btn-danger">Disconnect</button>
</div>`

// HandleMonarchToken handles POST /api/monarch/token — saves a manually-provided token.
func (h *Handler) HandleMonarchToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.settingsStore == nil {
		http.Error(w, "settings not available", http.StatusInternalServerError)
		return
	}

	token := strings.TrimSpace(r.FormValue("monarch_token"))
	if token == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Token is required.</div>`)
		return
	}

	// Create syncer from token to verify it works
	syncer, err := monarch.NewSyncer(token, "")
	if err != nil {
		h.logger.Printf("monarch token invalid: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="alert alert-error">Invalid token: %s</div>`, err.Error())
		return
	}

	if err := h.settingsStore.SetSetting(r.Context(), "monarch_token", token); err != nil {
		h.logger.Printf("failed to save monarch token: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Failed to save token.</div>`)
		return
	}

	h.monarchSyncer = syncer
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, monarchConnectedHTML)
}

// HandleMonarchDisconnect handles POST /api/monarch/disconnect — removes the token.
func (h *Handler) HandleMonarchDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.settingsStore == nil {
		http.Error(w, "settings not available", http.StatusInternalServerError)
		return
	}

	if err := h.settingsStore.SetSetting(r.Context(), "monarch_token", ""); err != nil {
		h.logger.Printf("failed to clear monarch token: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Failed to disconnect.</div>`)
		return
	}

	// Clear syncer so the background loop stops syncing too
	h.SetMonarchSyncer(nil)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<div class="alert alert-success">Monarch disconnected.</div>`)
}

// HandleMonarchSync handles POST /api/monarch/sync — triggers a manual sync.
func (h *Handler) HandleMonarchSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Create syncer on-demand from stored token if not already set
	if h.monarchSyncer == nil && h.settingsStore != nil {
		token, _ := h.settingsStore.GetSetting(r.Context(), "monarch_token")
		if token != "" {
			syncer, err := monarch.NewSyncer(token, "")
			if err == nil {
				h.monarchSyncer = syncer
			}
		}
	}

	if h.monarchSyncer == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Monarch not connected. Log in first.</div>`)
		return
	}

	// Get total BTC from portfolio
	totalSats, err := h.getTotalSats(r.Context())
	if err != nil {
		h.logger.Printf("monarch sync: failed to get total sats: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Failed to calculate total BTC.</div>`)
		return
	}

	totalBTC := float64(totalSats) / 1e8

	if err := h.monarchSyncer.SyncHolding(r.Context(), totalBTC); err != nil {
		h.logger.Printf("monarch sync failed: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="alert alert-error">Sync failed: %s</div>`, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="alert alert-success">Synced %.8f BTC to Monarch Money as a holding.</div>`, totalBTC)
}

// getTotalSats returns the total sats across all sources.
func (h *Handler) getTotalSats(ctx context.Context) (int64, error) {
	var total int64

	// On-chain wallet balance
	if bal, err := h.store.LatestWalletBalance(ctx); err == nil && bal != nil {
		total += bal.TotalSat
	}

	// Exchange balances
	for _, src := range []string{"strike", "river", "coinbase", "swan"} {
		if bal, err := h.store.ExchangeBalance(ctx, src); err == nil {
			total += bal
		}
	}

	// Cold storage (watched wallets)
	if h.walletStore != nil {
		if bal, err := h.walletStore.TotalWatchedBalance(ctx); err == nil {
			total += bal
		}
	}

	return total, nil
}

// HandleMonarchSyncTypes handles POST /api/monarch/sync-types — saves which tx types to sync.
func (h *Handler) HandleMonarchSyncTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.settingsStore == nil {
		http.Error(w, "settings not available", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--red);">Failed to parse form.</div>`)
		return
	}

	selectedTypes := r.Form["sync_types"]
	selectedSources := r.Form["sync_sources"]

	if err := h.settingsStore.SetSetting(r.Context(), "monarch_sync_types", strings.Join(selectedTypes, ",")); err != nil {
		h.logger.Printf("monarch: failed to save sync types: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--red);">Failed to save preferences.</div>`)
		return
	}
	if err := h.settingsStore.SetSetting(r.Context(), "monarch_sync_sources", strings.Join(selectedSources, ",")); err != nil {
		h.logger.Printf("monarch: failed to save sync sources: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--red);">Failed to save preferences.</div>`)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(selectedTypes) == 0 && len(selectedSources) == 0 {
		fmt.Fprint(w, `<div style="color:var(--green);">Transaction sync disabled — no filters selected.</div>`)
	} else {
		fmt.Fprintf(w, `<div style="color:var(--green);">Saved preferences: %d type(s), %d source(s).</div>`, len(selectedTypes), len(selectedSources))
	}
}

// HandleMonarchTxSync handles POST /api/monarch/tx-sync — syncs transactions to Monarch.
func (h *Handler) HandleMonarchTxSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Ensure Monarch syncer is available
	if h.monarchSyncer == nil && h.settingsStore != nil {
		token, _ := h.settingsStore.GetSetting(r.Context(), "monarch_token")
		if token != "" {
			syncer, err := monarch.NewSyncer(token, "")
			if err == nil {
				h.monarchSyncer = syncer
			}
		}
	}
	if h.monarchSyncer == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--red);">Monarch not connected. Log in first.</div>`)
		return
	}

	if h.settingsStore == nil || h.monarchTxStore == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--red);">Transaction sync not available.</div>`)
		return
	}

	// Read selected sync types and sources
	syncTypes, _ := h.settingsStore.GetSetting(r.Context(), "monarch_sync_types")
	if syncTypes == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--red);">No transaction types selected. Save preferences first.</div>`)
		return
	}

	types := strings.Split(syncTypes, ",")
	var sources []string
	if v, _ := h.settingsStore.GetSetting(r.Context(), "monarch_sync_sources"); v != "" {
		sources = strings.Split(v, ",")
	}

	// Get unsynced transactions
	txns, err := h.monarchTxStore.ListUnsyncedTransactions(r.Context(), types, sources)
	if err != nil {
		h.logger.Printf("monarch tx sync: list unsynced: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--red);">Failed to query transactions.</div>`)
		return
	}

	if len(txns) == 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div style="color:var(--green);">All transactions are already synced.</div>`)
		return
	}

	// Convert to TxToSync
	toSync := make([]monarch.TxToSync, len(txns))
	for i, tx := range txns {
		toSync[i] = monarch.TxToSync{
			SourceID:  tx.SourceID,
			Source:    tx.Source,
			TxType:    tx.TxType,
			Time:      tx.Time,
			AmountUSD: tx.AmountUSD,
			Memo:      tx.Memo,
		}
	}

	// Respond immediately — sync runs in the background
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div style="color:var(--accent);">Syncing %d transactions to Monarch in the background. You can leave this page.</div>`, len(toSync))

	// Capture references for the goroutine
	syncer := h.monarchSyncer
	txStore := h.monarchTxStore
	logger := h.logger

	go func() {
		ctx := context.Background()
		result, synced, err := syncer.SyncTransactions(ctx, toSync)
		if err != nil {
			logger.Printf("monarch tx sync failed: %v", err)
			return
		}

		for sourceID, monarchTxID := range synced {
			if err := txStore.MarkTransactionSynced(ctx, sourceID, monarchTxID); err != nil {
				logger.Printf("monarch: failed to mark %s synced: %v", sourceID, err)
			}
		}

		logger.Printf("monarch tx sync complete: %d created, %d skipped, %d errors", result.Created, result.Skipped, result.Errors)
	}()
}

// maskToken shows only the last 4 characters of a token.
func maskToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return strings.Repeat("*", 8) + token[len(token)-4:]
}

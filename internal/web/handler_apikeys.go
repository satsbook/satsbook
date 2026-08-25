package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/satsbook/satsbook/internal/apikey"
	"github.com/satsbook/satsbook/internal/db"
)

// HandleAPIKeyList handles GET /api/settings/apikeys/list — returns the key table fragment.
func (h *Handler) HandleAPIKeyList(w http.ResponseWriter, r *http.Request) {
	if h.apiKeyStore == nil {
		http.Error(w, "not available", http.StatusServiceUnavailable)
		return
	}
	keys, err := h.apiKeyStore.ListAPIKeys(r.Context())
	if err != nil {
		h.logger.Printf("api keys list: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAPIKeySection(w, keys, "")
}

// HandleAPIKeyCreate handles POST /api/settings/apikeys — creates a new API key.
func (h *Handler) HandleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.apiKeyStore == nil {
		http.Error(w, "not available", http.StatusServiceUnavailable)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Key name is required.</div>`)
		return
	}

	raw, hash, prefix, err := apikey.Generate()
	if err != nil {
		h.logger.Printf("api key generate: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Failed to generate key.</div>`)
		return
	}

	if _, err = h.apiKeyStore.CreateAPIKey(r.Context(), name, hash, prefix); err != nil {
		h.logger.Printf("api key create: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Failed to save key.</div>`)
		return
	}

	keys, _ := h.apiKeyStore.ListAPIKeys(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAPIKeySection(w, keys, raw)
}

// HandleAPIKeyRevoke handles POST /api/settings/apikeys/revoke — revokes a key by ID.
func (h *Handler) HandleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.apiKeyStore == nil {
		http.Error(w, "not available", http.StatusServiceUnavailable)
		return
	}

	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Invalid key ID.</div>`)
		return
	}

	if err := h.apiKeyStore.RevokeAPIKey(r.Context(), id); err != nil {
		h.logger.Printf("api key revoke: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">Failed to revoke key.</div>`)
		return
	}

	keys, _ := h.apiKeyStore.ListAPIKeys(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAPIKeySection(w, keys, "")
}

// renderAPIKeySection writes the full developer key management HTML fragment.
// newRaw is the plaintext key shown once after creation (empty otherwise).
func renderAPIKeySection(w http.ResponseWriter, keys []db.APIKey, newRaw string) {
	fmt.Fprint(w, `<div id="apikey-section">`)

	if newRaw != "" {
		fmt.Fprintf(w, `
<div class="alert alert-success" style="margin-bottom:12px;">
  API key created. Copy it now — it will not be shown again.<br>
  <code style="font-size:0.8rem;word-break:break-all;user-select:all;">%s</code>
</div>`, newRaw)
	}

	fmt.Fprint(w, `
<form hx-post="/api/settings/apikeys" hx-target="#apikey-section" hx-swap="outerHTML"
      style="display:flex;gap:8px;margin-bottom:16px;max-width:480px;">
  <input type="text" name="name" placeholder="e.g. Google Sheets" required
         style="flex:1;" autocomplete="off">
  <button type="submit"
          style="background:var(--accent);color:#000;border:none;border-radius:4px;padding:6px 14px;font-size:0.85rem;font-weight:600;cursor:pointer;">
    Create Key
  </button>
</form>`)

	if len(keys) == 0 {
		fmt.Fprint(w, `<p style="font-size:0.85rem;color:var(--text-muted);">No API keys yet.</p>`)
	} else {
		fmt.Fprint(w, `<table style="width:100%;border-collapse:collapse;font-size:0.85rem;">
<thead><tr>
  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--border);">Name</th>
  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--border);">Prefix</th>
  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--border);">Created</th>
  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--border);">Last used</th>
  <th style="padding:6px 8px;border-bottom:1px solid var(--border);"></th>
</tr></thead><tbody>`)

		for _, k := range keys {
			lastUsed := "—"
			if k.LastUsedAt != nil {
				lastUsed = k.LastUsedAt.Format("2006-01-02")
			}
			fmt.Fprintf(w, `<tr>
  <td style="padding:6px 8px;">%s</td>
  <td style="padding:6px 8px;font-family:monospace;">%s…</td>
  <td style="padding:6px 8px;">%s</td>
  <td style="padding:6px 8px;">%s</td>
  <td style="padding:6px 8px;">
    <button hx-post="/api/settings/apikeys/revoke" hx-vals='{"id":"%d"}'
            hx-target="#apikey-section" hx-swap="outerHTML"
            hx-confirm="Revoke this key? Any scripts using it will stop working."
            style="background:none;border:1px solid var(--border);border-radius:4px;padding:3px 10px;font-size:0.8rem;cursor:pointer;color:var(--text-muted);">
      Revoke
    </button>
  </td>
</tr>`,
				k.Name, k.KeyPrefix, k.CreatedAt.Format("2006-01-02"), lastUsed, k.ID)
		}
		fmt.Fprint(w, `</tbody></table>`)
	}

	fmt.Fprint(w, `</div>`)
}

package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/satsbook/satsbook/internal/license"
)

// demoserver runs a fake license validation server for testing.
//
// Usage:
//   go run ./cmd/demoserver <real|fake> [port]
//
// "real" uses the project's actual private key (tokens will be accepted).
// "fake" generates a random keypair (tokens will be rejected by the app).

// Set via SATSBOOK_LICENSE_SIGNING_KEY env var for "real" mode.
var realPrivKeyHex = os.Getenv("SATSBOOK_LICENSE_SIGNING_KEY")

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: demoserver <real|fake> [port]\n")
		os.Exit(1)
	}

	mode := os.Args[1]
	port := "3098"
	if len(os.Args) > 2 {
		port = os.Args[2]
	}

	var priv ed25519.PrivateKey

	switch mode {
	case "real":
		if realPrivKeyHex == "" {
			fmt.Fprintf(os.Stderr, "SATSBOOK_LICENSE_SIGNING_KEY env var required for real mode\n")
			os.Exit(1)
		}
		privBytes, err := hex.DecodeString(realPrivKeyHex)
		if err != nil || len(privBytes) != ed25519.PrivateKeySize {
			fmt.Fprintf(os.Stderr, "invalid SATSBOOK_LICENSE_SIGNING_KEY (expected %d hex-encoded bytes)\n", ed25519.PrivateKeySize)
			os.Exit(1)
		}
		priv = ed25519.PrivateKey(privBytes)
		log.Printf("REAL server: signing with the project's private key")
	case "fake":
		_, fakePriv, _ := ed25519.GenerateKey(nil)
		priv = fakePriv
		log.Printf("FAKE server: signing with a random key (will be rejected)")
	default:
		fmt.Fprintf(os.Stderr, "mode must be 'real' or 'fake'\n")
		os.Exit(1)
	}

	http.HandleFunc("/v1/license/validate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key string `json:"key"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		log.Printf("received validation request for key: %s", req.Key)

		tier := "power" // always return power for demo
		token, err := license.SignToken(license.TokenPayload{
			Key:       req.Key,
			Tier:      tier,
			ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
		}, priv)
		if err != nil {
			log.Printf("sign error: %v", err)
			http.Error(w, "internal error", 500)
			return
		}

		resp := map[string]interface{}{
			"valid": true,
			"tier":  tier,
			"token": token,
		}
		log.Printf("responding: valid=true tier=%s mode=%s", tier, mode)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	addr := ":" + port
	log.Printf("demo validation server listening on %s (mode=%s)", addr, mode)
	log.Fatal(http.ListenAndServe(addr, nil))
}

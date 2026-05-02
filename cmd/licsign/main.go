package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/satsbook/satsbook/internal/license"
)

// licsign generates Ed25519 keypairs and signs license tokens for testing.
//
// Usage:
//   go run ./cmd/licsign keygen                              # generate a new keypair
//   go run ./cmd/licsign sign <privkey-hex> <tier> <key> [days]  # sign a token

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "keygen":
		keygen()
	case "sign":
		if len(os.Args) < 5 {
			fmt.Fprintf(os.Stderr, "usage: licsign sign <privkey-hex> <tier> <license-key> [days]\n")
			os.Exit(1)
		}
		sign(os.Args[2], os.Args[3], os.Args[4])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  licsign keygen\n")
	fmt.Fprintf(os.Stderr, "  licsign sign <privkey-hex> <tier> <license-key> [days]\n")
}

func keygen() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "keygen failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Private key (keep secret): %s\n", hex.EncodeToString(priv))
	fmt.Printf("Public key (embed in app): %s\n", hex.EncodeToString(pub))
}

func sign(privKeyHex, tier, licenseKey string) {
	privBytes, err := hex.DecodeString(privKeyHex)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		fmt.Fprintf(os.Stderr, "invalid private key hex (expected %d bytes)\n", ed25519.PrivateKeySize)
		os.Exit(1)
	}
	priv := ed25519.PrivateKey(privBytes)

	if !license.ValidTier(tier) {
		fmt.Fprintf(os.Stderr, "invalid tier %q (use free, pro, or power)\n", tier)
		os.Exit(1)
	}

	days := 30
	if len(os.Args) > 5 {
		d, err := strconv.Atoi(os.Args[5])
		if err != nil || d < 1 {
			fmt.Fprintf(os.Stderr, "invalid days: %s\n", os.Args[5])
			os.Exit(1)
		}
		days = d
	}

	payload := license.TokenPayload{
		Key:       licenseKey,
		Tier:      tier,
		ExpiresAt: time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix(),
	}

	token, err := license.SignToken(payload, priv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Token (valid %d days):\n%s\n", days, token)
}

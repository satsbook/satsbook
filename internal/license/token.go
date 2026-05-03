package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TokenPayload is the signed content of a license token.
type TokenPayload struct {
	Key       string `json:"key"`
	Tier      string `json:"tier"`
	ExpiresAt int64  `json:"exp"`
}

// SignToken creates a signed token string: base64(payload).base64(signature).
func SignToken(payload TokenPayload, privateKey ed25519.PrivateKey) (string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)
	sig := ed25519.Sign(privateKey, payloadBytes)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return payloadB64 + "." + sigB64, nil
}

// VerifyToken parses and verifies a signed token against the public key.
// Returns the payload if valid, or an error if the signature is invalid or the token is expired.
func VerifyToken(token string, publicKey ed25519.PublicKey) (*TokenPayload, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	if !ed25519.Verify(publicKey, payloadBytes, sig) {
		return nil, fmt.Errorf("invalid signature")
	}

	var payload TokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	if payload.ExpiresAt > 0 && time.Unix(payload.ExpiresAt, 0).Before(time.Now()) {
		return nil, fmt.Errorf("token expired")
	}

	return &payload, nil
}

// VerifyTokenAt is like VerifyToken but uses a custom time for expiry checks (for testing).
func VerifyTokenAt(token string, publicKey ed25519.PublicKey, now time.Time) (*TokenPayload, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	if !ed25519.Verify(publicKey, payloadBytes, sig) {
		return nil, fmt.Errorf("invalid signature")
	}

	var payload TokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	if payload.ExpiresAt > 0 && time.Unix(payload.ExpiresAt, 0).Before(now) {
		return nil, fmt.Errorf("token expired")
	}

	return &payload, nil
}

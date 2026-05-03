package license

import (
	"crypto/ed25519"
	"encoding/hex"
)

// publicKeyHex is the Ed25519 public key used to verify license tokens.
// The corresponding private key is kept on the satsbook validation server.
// To rotate: generate a new keypair with `go run ./cmd/licsign keygen`,
// update this value, and deploy the new private key to the server.
const publicKeyHex = "0f0f19de4a7ad68955ee477d9e19a6a08a47569f6240c7ae62858b86f4321eee"

// PublicKey returns the embedded Ed25519 public key for token verification.
func PublicKey() ed25519.PublicKey {
	key, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		panic("license: invalid embedded public key: " + err.Error())
	}
	return ed25519.PublicKey(key)
}

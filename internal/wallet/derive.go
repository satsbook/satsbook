package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/mr-tron/base58"
)

// DerivationType identifies the BIP standard for address derivation.
type DerivationType string

const (
	DeriveBIP44 DerivationType = "bip44" // xpub → P2PKH (1...)
	DeriveBIP49 DerivationType = "bip49" // ypub → P2SH-P2WPKH (3...)
	DeriveBIP84 DerivationType = "bip84" // zpub → P2WPKH (bc1q...)
)

// Version bytes for extended public keys.
var (
	xpubVersion = [4]byte{0x04, 0x88, 0xB2, 0x1E}
	ypubVersion = [4]byte{0x04, 0x9D, 0x7C, 0xB2}
	zpubVersion = [4]byte{0x04, 0xB2, 0x47, 0x46}
)

// NormalizeKey detects the key prefix (xpub/ypub/zpub), converts it to xpub
// format for hdkeychain compatibility, and returns the derivation type.
func NormalizeKey(key string) (xpub string, derivationType DerivationType, err error) {
	if len(key) < 4 {
		return "", "", fmt.Errorf("key too short")
	}

	decoded, err := decodeBase58(key)
	if err != nil {
		return "", "", fmt.Errorf("invalid base58 encoding: %w", err)
	}
	if len(decoded) < 4 {
		return "", "", fmt.Errorf("decoded key too short")
	}

	var version [4]byte
	copy(version[:], decoded[:4])

	switch version {
	case xpubVersion:
		return key, DeriveBIP44, nil
	case ypubVersion:
		copy(decoded[:4], xpubVersion[:])
		return encodeBase58Check(decoded), DeriveBIP49, nil
	case zpubVersion:
		copy(decoded[:4], xpubVersion[:])
		return encodeBase58Check(decoded), DeriveBIP84, nil
	default:
		return "", "", fmt.Errorf("unsupported key prefix: %x", version)
	}
}

// DeriveAddresses derives Bitcoin addresses from an extended public key
// on the specified branch (0 = external, 1 = change) starting at the
// given index and returning count addresses.
func DeriveAddresses(xpub string, branch uint32, start, count int, derivationType DerivationType) ([]string, error) {
	masterKey, err := hdkeychain.NewKeyFromString(xpub)
	if err != nil {
		return nil, fmt.Errorf("parse xpub: %w", err)
	}

	branchKey, err := masterKey.Derive(branch)
	if err != nil {
		return nil, fmt.Errorf("derive branch %d: %w", branch, err)
	}

	addresses := make([]string, 0, count)
	for i := start; i < start+count; i++ {
		child, err := branchKey.Derive(uint32(i))
		if err != nil {
			return nil, fmt.Errorf("derive index %d: %w", i, err)
		}

		addr, err := addressFromKey(child, derivationType)
		if err != nil {
			return nil, fmt.Errorf("address from key at index %d: %w", i, err)
		}

		addresses = append(addresses, addr)
	}

	return addresses, nil
}

// AddressToScriptHash converts a Bitcoin address to an Electrum-compatible
// script hash (SHA256 of the output script, reversed).
func AddressToScriptHash(address string) (string, error) {
	addr, err := btcutil.DecodeAddress(address, &chaincfg.MainNetParams)
	if err != nil {
		return "", fmt.Errorf("decode address %q: %w", address, err)
	}

	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return "", fmt.Errorf("pay to addr script: %w", err)
	}

	hash := sha256.Sum256(script)
	// Reverse the hash bytes for Electrum protocol
	for i, j := 0, len(hash)-1; i < j; i, j = i+1, j-1 {
		hash[i], hash[j] = hash[j], hash[i]
	}

	return hex.EncodeToString(hash[:]), nil
}

// addressFromKey creates an address from an HD key based on derivation type.
func addressFromKey(key *hdkeychain.ExtendedKey, dt DerivationType) (string, error) {
	pubKey, err := key.ECPubKey()
	if err != nil {
		return "", fmt.Errorf("get public key: %w", err)
	}

	pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())

	switch dt {
	case DeriveBIP84:
		// P2WPKH (bc1q...)
		addr, err := btcutil.NewAddressWitnessPubKeyHash(pubKeyHash, &chaincfg.MainNetParams)
		if err != nil {
			return "", err
		}
		return addr.EncodeAddress(), nil

	case DeriveBIP49:
		// P2SH-P2WPKH (3...)
		witnessAddr, err := btcutil.NewAddressWitnessPubKeyHash(pubKeyHash, &chaincfg.MainNetParams)
		if err != nil {
			return "", err
		}
		witnessScript, err := txscript.PayToAddrScript(witnessAddr)
		if err != nil {
			return "", err
		}
		addr, err := btcutil.NewAddressScriptHash(witnessScript, &chaincfg.MainNetParams)
		if err != nil {
			return "", err
		}
		return addr.EncodeAddress(), nil

	case DeriveBIP44:
		// P2PKH (1...)
		addr, err := btcutil.NewAddressPubKeyHash(pubKeyHash, &chaincfg.MainNetParams)
		if err != nil {
			return "", err
		}
		return addr.EncodeAddress(), nil

	default:
		return "", fmt.Errorf("unsupported derivation type: %s", dt)
	}
}

// decodeBase58 decodes a base58check-encoded extended key and returns the raw
// 78-byte payload (4-byte version + 74-byte data) after stripping the checksum.
func decodeBase58(s string) ([]byte, error) {
	decoded, err := base58.Decode(s)
	if err != nil {
		return nil, err
	}
	// base58check format: [78 bytes payload][4 bytes checksum]
	if len(decoded) < 82 {
		return nil, fmt.Errorf("decoded length %d too short for extended key", len(decoded))
	}
	payload := decoded[:78]
	// Verify checksum
	cksum1 := sha256.Sum256(payload)
	cksum2 := sha256.Sum256(cksum1[:])
	for i := 0; i < 4; i++ {
		if decoded[78+i] != cksum2[i] {
			return nil, fmt.Errorf("checksum mismatch")
		}
	}
	return payload, nil
}

// encodeBase58Check encodes a 78-byte extended key payload with a base58check checksum.
func encodeBase58Check(payload []byte) string {
	cksum1 := sha256.Sum256(payload)
	cksum2 := sha256.Sum256(cksum1[:])
	full := make([]byte, len(payload)+4)
	copy(full, payload)
	copy(full[len(payload):], cksum2[:4])
	return base58.Encode(full)
}

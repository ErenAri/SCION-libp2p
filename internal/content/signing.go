package content

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
)

// SignManifest signs a manifest's content with an Ed25519 private key,
// producing a hex-encoded signature stored in the manifest.
func SignManifest(m *Manifest, privKey ed25519.PrivateKey) {
	msg := manifestSigningPayload(m)
	sig := ed25519.Sign(privKey, msg)
	m.PublisherID = hex.EncodeToString(privKey.Public().(ed25519.PublicKey))
	m.Signature = hex.EncodeToString(sig)
}

// VerifyManifest verifies a manifest's Ed25519 signature.
// Returns nil if valid, error otherwise.
func VerifyManifest(m *Manifest) error {
	if m.Signature == "" || m.PublisherID == "" {
		return fmt.Errorf("manifest is not signed")
	}

	pubKeyBytes, err := hex.DecodeString(m.PublisherID)
	if err != nil {
		return fmt.Errorf("decode publisher ID: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid publisher key size: %d", len(pubKeyBytes))
	}

	sig, err := hex.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	msg := manifestSigningPayload(m)
	pubKey := ed25519.PublicKey(pubKeyBytes)

	if !ed25519.Verify(pubKey, msg, sig) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// manifestSigningPayload builds the deterministic payload to sign.
func manifestSigningPayload(m *Manifest) []byte {
	// Sign over: RootCID + Name + all ChunkCIDs (sorted by index).
	parts := []string{m.RootCID, m.Name}
	parts = append(parts, m.ChunkCIDs...)
	return []byte(strings.Join(parts, "|"))
}

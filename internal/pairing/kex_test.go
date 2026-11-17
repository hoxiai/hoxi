package pairing

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"testing"
)

func TestX25519KEXRoundTripAndTamperRejection(t *testing.T) {
	svc := NewService(filepath.Join(t.TempDir(), "pairing.json"), 5417)
	wantKeyHex, err := svc.E2EEKey()
	if err != nil {
		t.Fatalf("E2EEKey: %v", err)
	}

	clientPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	clientPubB64 := base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())

	bundle, err := svc.SealE2EEKeyForClientPub(clientPubB64)
	if err != nil {
		t.Fatalf("SealE2EEKeyForClientPub: %v", err)
	}
	if bundle.Algorithm != KEXAlgorithm || bundle.ServerPub == "" || bundle.SealedKey == "" {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}

	gotHex, err := OpenE2EEKeyFromKEX(clientPriv, *bundle)
	if err != nil {
		t.Fatalf("OpenE2EEKeyFromKEX: %v", err)
	}
	if gotHex != wantKeyHex {
		t.Fatalf("unwrapped key = %q, want %q", gotHex, wantKeyHex)
	}

	// Wrong private key must fail AEAD authentication.
	attackerPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	if _, err := OpenE2EEKeyFromKEX(attackerPriv, *bundle); err == nil {
		t.Fatal("expected attacker private key to fail decapsulation")
	}

	// All-zero low-order X25519 public key must be rejected.
	zeroPubB64 := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := svc.SealE2EEKeyForClientPub(zeroPubB64); err == nil {
		t.Fatal("expected all-zero low-order X25519 key to be rejected")
	}
}

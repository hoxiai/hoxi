package pairing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	// KEXClientPubHeader carries the mobile client's ephemeral 32-byte X25519
	// public key (base64url unpadded) on authenticated bootstrap requests when
	// the controller does not yet hold the desktop's E2EE master key.
	KEXClientPubHeader = "X-Hoxi-KEX-Pub"

	// KEXAlgorithm identifies the X25519 + HMAC-SHA256 + AES-256-GCM KEM suite.
	KEXAlgorithm = "X25519-AES256GCM-v1"
)

var ErrInvalidKEXPublicKey = errors.New("invalid x25519 public key")

// E2EEKEXBundle is returned inside /api/hoxi/bootstrap when KEXClientPubHeader
// is supplied, encapsulating the 32-byte E2EE master key so passive relays
// never observe plaintext key material.
type E2EEKEXBundle struct {
	Algorithm string `json:"alg"`
	ServerPub string `json:"serverPub"`
	Nonce     string `json:"nonce"`
	SealedKey string `json:"sealedKey"`
}

func deriveKEXWrapKey(sharedSecret, clientPub, serverPub []byte) []byte {
	mac := hmac.New(sha256.New, sharedSecret)
	_, _ = mac.Write([]byte("hoxi-kex-x25519-v1|"))
	_, _ = mac.Write(clientPub)
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write(serverPub)
	return mac.Sum(nil)
}

func kexAAD(clientPubB64, serverPubB64 string) []byte {
	return []byte("hoxi-kex-v1:" + clientPubB64 + ":" + serverPubB64)
}

// SealE2EEKeyForClientPub encapsulates this desktop's 32-byte E2EE master key
// to the caller's ephemeral X25519 public key.
func (s *Service) SealE2EEKeyForClientPub(clientPubB64 string) (*E2EEKEXBundle, error) {
	e2eeHex, err := s.E2EEKey()
	if err != nil {
		return nil, err
	}
	masterBytes, err := hex.DecodeString(strings.TrimSpace(e2eeHex))
	if err != nil || len(masterBytes) != 32 {
		return nil, ErrInvalidE2EEKey
	}
	return SealMasterKeyX25519(masterBytes, clientPubB64)
}

// SealMasterKeyX25519 encapsulates a 32-byte master key to clientPubB64 using
// ephemeral X25519 ECDH and AES-256-GCM.
func SealMasterKeyX25519(masterKey32 []byte, clientPubB64 string) (*E2EEKEXBundle, error) {
	if len(masterKey32) != 32 {
		return nil, ErrInvalidE2EEKey
	}
	cleanClientB64 := strings.TrimRight(strings.TrimSpace(clientPubB64), "=")
	rawClientPub, err := base64.RawURLEncoding.DecodeString(cleanClientB64)
	if err != nil || len(rawClientPub) != 32 {
		return nil, ErrInvalidKEXPublicKey
	}
	curve := ecdh.X25519()
	clientPub, err := curve.NewPublicKey(rawClientPub)
	if err != nil {
		return nil, ErrInvalidKEXPublicKey
	}
	serverPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := serverPriv.ECDH(clientPub)
	if err != nil {
		return nil, ErrInvalidKEXPublicKey
	}
	rawServerPub := serverPriv.PublicKey().Bytes()
	serverPubB64 := base64.RawURLEncoding.EncodeToString(rawServerPub)
	canonicalClientB64 := base64.RawURLEncoding.EncodeToString(rawClientPub)

	wrapKey := deriveKEXWrapKey(shared, rawClientPub, rawServerPub)
	block, err := aes.NewCipher(wrapKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, e2eeNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nil, nonce, masterKey32, kexAAD(canonicalClientB64, serverPubB64))
	return &E2EEKEXBundle{
		Algorithm: KEXAlgorithm,
		ServerPub: serverPubB64,
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
		SealedKey: base64.RawURLEncoding.EncodeToString(sealed),
	}, nil
}

// OpenE2EEKeyFromKEX decapsulates an E2EEKEXBundle using the client's X25519
// private key and returns the 64-char hex E2EE master key.
func OpenE2EEKeyFromKEX(clientPriv *ecdh.PrivateKey, bundle E2EEKEXBundle) (string, error) {
	if clientPriv == nil || bundle.Algorithm != KEXAlgorithm {
		return "", ErrInvalidE2EEEnvelope
	}
	rawServerPub, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(bundle.ServerPub), "="))
	if err != nil || len(rawServerPub) != 32 {
		return "", ErrInvalidKEXPublicKey
	}
	nonce, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(bundle.Nonce), "="))
	if err != nil || len(nonce) != e2eeNonceSize {
		return "", ErrInvalidE2EEEnvelope
	}
	ct, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(bundle.SealedKey), "="))
	if err != nil {
		return "", ErrInvalidE2EEEnvelope
	}
	serverPub, err := ecdh.X25519().NewPublicKey(rawServerPub)
	if err != nil {
		return "", ErrInvalidKEXPublicKey
	}
	shared, err := clientPriv.ECDH(serverPub)
	if err != nil {
		return "", ErrInvalidKEXPublicKey
	}
	rawClientPub := clientPriv.PublicKey().Bytes()
	clientB64 := base64.RawURLEncoding.EncodeToString(rawClientPub)
	serverB64 := base64.RawURLEncoding.EncodeToString(rawServerPub)

	wrapKey := deriveKEXWrapKey(shared, rawClientPub, rawServerPub)
	block, err := aes.NewCipher(wrapKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := aead.Open(nil, nonce, ct, kexAAD(clientB64, serverB64))
	if err != nil || len(plain) != 32 {
		return "", ErrInvalidE2EEEnvelope
	}
	return hex.EncodeToString(plain), nil
}

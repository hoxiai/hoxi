package gateway

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/hoxiai/hoxi/internal/pairing"
)

type testX25519Client struct {
	priv   *ecdh.PrivateKey
	pubB64 string
}

func ecdhGenerateX25519ForTest(t *testing.T) (*testX25519Client, error) {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &testX25519Client{
		priv:   priv,
		pubB64: base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
	}, nil
}

func (c *testX25519Client) open(bundle pairing.E2EEKEXBundle) (string, error) {
	return pairing.OpenE2EEKeyFromKEX(c.priv, bundle)
}

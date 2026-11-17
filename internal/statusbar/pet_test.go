package statusbar

import (
	"encoding/base64"
	"testing"
)

func TestDecodePetAvatarDataURLAndCache(t *testing.T) {
	resetPetAvatarCacheForTest()

	rawPNG := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(rawPNG)

	decoded, ok := DecodePetAvatarDataURL(dataURL)
	if !ok || len(decoded) != len(rawPNG) {
		t.Fatalf("expected valid PNG data URL decode, got ok=%v len=%d", ok, len(decoded))
	}
	if _, ok := DecodePetAvatarDataURL("https://example.com/a.png"); ok {
		t.Fatal("expected non-data URL to be rejected")
	}

	bytes1, changed1, need1 := resolvePetAvatarCache("hoxi-chan:idle:#2563eb", "")
	if !need1 || changed1 || len(bytes1) != 0 {
		t.Fatalf("expected initial cache miss to request avatar: need=%v changed=%v", need1, changed1)
	}

	bytes2, changed2, need2 := resolvePetAvatarCache("hoxi-chan:idle:#2563eb", dataURL)
	if need2 || !changed2 || len(bytes2) != len(rawPNG) {
		t.Fatalf("expected cache populate: need=%v changed=%v len=%d", need2, changed2, len(bytes2))
	}

	bytes3, changed3, need3 := resolvePetAvatarCache("hoxi-chan:idle:#2563eb", "")
	if need3 || changed3 || bytes3 != nil {
		t.Fatalf("expected silent cache hit: need=%v changed=%v", need3, changed3)
	}
}

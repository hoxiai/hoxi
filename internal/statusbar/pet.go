package statusbar

import (
	"encoding/base64"
	"strings"
	"sync"
)

const (
	MenuActionCompanionTap       = "companion-tap"
	MenuActionCompanionToggleMin = "companion-toggle-min"
	MenuActionCompanionDismiss   = "companion-dismiss"

	maxPetAvatarBytes = 16 << 20
)

// PetPayload describes the live state of the floating desktop pet synced from
// the frontend companion store.
type PetPayload struct {
	Visible         bool   `json:"visible"`
	Minimized       bool   `json:"minimized"`
	DarkTheme       bool   `json:"darkTheme"`
	Persona         string `json:"persona"`
	PersonaName     string `json:"personaName"`
	Mood            string `json:"mood"`
	BadgeLabel      string `json:"badgeLabel"`
	LevelText       string `json:"levelText"`
	Dialogue        string `json:"dialogue"`
	ShowBubble      bool   `json:"showBubble"`
	TargetSessionID string `json:"targetSessionID"`
	AccentHex       string `json:"accentHex"`
	AvatarKey       string `json:"avatarKey"`
	AvatarDataURL   string `json:"avatarDataURL,omitempty"`
}

// PetSyncResult reports whether the native floating window host is supported
// and whether the caller should resend AvatarDataURL on the next sync.
type PetSyncResult struct {
	Supported  bool `json:"supported"`
	NeedAvatar bool `json:"needAvatar"`
}

var (
	petAvatarMu    sync.Mutex
	petCachedKey   string
	petCachedBytes []byte
)

// DecodePetAvatarDataURL parses a data:image/*;base64,... URL into raw image bytes.
func DecodePetAvatarDataURL(raw string) ([]byte, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "data:image/") {
		return nil, false
	}
	comma := strings.IndexByte(trimmed, ',')
	if comma <= 0 {
		return nil, false
	}
	header := strings.ToLower(trimmed[:comma])
	if !strings.Contains(header, ";base64") {
		return nil, false
	}
	b64 := trimmed[comma+1:]
	if len(b64) == 0 || len(b64) > (maxPetAvatarBytes*4/3)+64 {
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(decoded) == 0 || len(decoded) > maxPetAvatarBytes {
		return nil, false
	}
	return decoded, true
}

func resolvePetAvatarCache(key, dataURL string) (avatarBytes []byte, avatarChanged bool, needAvatar bool) {
	petAvatarMu.Lock()
	defer petAvatarMu.Unlock()

	cleanKey := strings.TrimSpace(key)
	if cleanKey == "" {
		if petCachedKey != "" {
			petCachedKey = ""
			petCachedBytes = nil
			return nil, true, false
		}
		return nil, false, false
	}

	if dataURL != "" {
		if decoded, ok := DecodePetAvatarDataURL(dataURL); ok {
			changed := cleanKey != petCachedKey || len(decoded) != len(petCachedBytes)
			petCachedKey = cleanKey
			petCachedBytes = decoded
			return decoded, changed, false
		}
	}

	if cleanKey == petCachedKey && len(petCachedBytes) > 0 {
		return nil, false, false
	}

	return nil, false, true
}

func resetPetAvatarCacheForTest() {
	petAvatarMu.Lock()
	petCachedKey = ""
	petCachedBytes = nil
	petAvatarMu.Unlock()
}

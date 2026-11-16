package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTokenStoreSaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	store := &TokenStore{
		filePath: filepath.Join(tempDir, "auth.json"),
	}

	// 初始加载为空
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("unexpected error on initial load: %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected nil auth, got %+v", loaded)
	}

	auth := &StoredAuth{
		DeviceToken: "hox_dt_test_token_123456",
		User: UserProfile{
			ID:       101,
			Email:    "developer@hoxi.ai",
			Nickname: "Hoxi Dev",
			Role:     "user",
		},
		ExpiresAt:      "2026-12-31T23:59:59Z",
		LastVerifiedAt: time.Now().UTC().Truncate(time.Second),
	}

	if err := store.Save(auth); err != nil {
		t.Fatalf("failed to save auth: %v", err)
	}

	loaded, err = store.Load()
	if err != nil {
		t.Fatalf("failed to reload auth: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil loaded auth")
	}

	if loaded.DeviceToken != auth.DeviceToken {
		t.Errorf("expected token %s, got %s", auth.DeviceToken, loaded.DeviceToken)
	}
	if loaded.User.Email != auth.User.Email {
		t.Errorf("expected email %s, got %s", auth.User.Email, loaded.User.Email)
	}

	// 测试清空
	if err := store.Clear(); err != nil {
		t.Fatalf("failed to clear store: %v", err)
	}
	loaded, err = store.Load()
	if err != nil || loaded != nil {
		t.Fatalf("expected nil auth after clear, got %+v (err: %v)", loaded, err)
	}
}

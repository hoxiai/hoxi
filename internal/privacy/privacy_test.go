package privacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := New("")
	if got := s.Status(); got != (Status{Version: Version}) {
		t.Fatalf("default status: %+v", got)
	}
	for _, accepted := range []bool{true, false} {
		want := Status{Version: Version, Accepted: accepted}
		if err := s.Set(want); err != nil {
			t.Fatal(err)
		}
		if got := New("").Status(); got != want {
			t.Fatalf("reloaded status: %+v, want %+v", got, want)
		}
		info, err := os.Stat(filepath.Join(home, ".hoxi", "privacy.json"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("expected 0600 state: %v, %v", info, err)
		}
	}
}

func TestInvalidStateDenied(t *testing.T) {
	for _, input := range []string{
		`{`, `null`, `{}`, `{"version":"old","accepted":true}`,
		`{"version":"2026-10-18","accepted":null}`,
		`{"version":"2026-10-18","accepted":"true"}`,
		`{"version":"2026-10-18","accepted":true} {}`,
		`{"version":"2026-10-18","accepted":true,"extra":1}`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(input)); err == nil {
				t.Fatal("invalid consent decoded")
			}
			statePath := filepath.Join(t.TempDir(), "privacy.json")
			if err := os.WriteFile(statePath, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			if New(statePath).Status().Accepted {
				t.Fatal("invalid state accepted")
			}
		})
	}
}

func TestUnavailableStateDenied(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "privacy.json")
	s := New(statePath)
	if err := s.Set(Status{Version: Version, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0, 0644} {
		if err := os.Chmod(statePath, mode); err != nil {
			t.Fatal(err)
		}
		if s.Status().Accepted {
			t.Fatalf("accepted state with mode %o", mode)
		}
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if s.Status().Accepted {
		t.Fatal("deleted state accepted")
	}
	if err := os.Mkdir(statePath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(Status{Version: Version}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if s.Status().Accepted {
		t.Fatal("persistence failure must deny")
	}
	if err := s.Set(Status{Version: "old", Accepted: true}); err == nil {
		t.Fatal("unsupported version saved")
	}
}

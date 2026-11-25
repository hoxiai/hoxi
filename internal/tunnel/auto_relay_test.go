package tunnel

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestStartMigratesRetiredAutoRelayController(t *testing.T) {
	oldCtrl := newFakeController(t)
	newCtrl := newFakeController(t)
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	store := NewStore(configPath)
	if err := store.Save(Config{
		ControllerURL: oldCtrl.server.URL,
		NodeID:        5,
		ClientID:      testClientID,
		Token:         testToken,
		Name:          "Studio",
		AutoRelay:     true,
	}); err != nil {
		t.Fatal(err)
	}

	m := NewManager(Options{
		Handler:  http.NotFoundHandler(),
		Version:  "test",
		Hostname: func() string { return "mac.local" },
	})
	m.Configure(store, 5410)
	m.SetDefaultRelayURL(newCtrl.server.URL)
	m.SetRetiredAutoRelayURLs([]string{oldCtrl.server.URL + "/"})
	t.Cleanup(m.Stop)

	m.Start()
	conn := <-newCtrl.conns
	t.Cleanup(func() { _ = conn.Close() })

	if !waitFor(t, 5*time.Second, func() bool { return m.Status().State == StateOnline }) {
		t.Fatalf("state = %s, want online after auto-relay migration", m.Status().State)
	}
	if !m.RequiresPairingToken() {
		t.Fatal("migrated auto-relay tunnel must keep requiring pairing token")
	}
	wantProxy := newCtrl.server.URL + "/api/tunnel/hoxi-nodes/9/proxy"
	if got := m.RelayProxyURL(); got != wantProxy {
		t.Fatalf("RelayProxyURL() = %q, want %q", got, wantProxy)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("load migrated config: %v", err)
	}
	if saved.ControllerURL != newCtrl.server.URL || !saved.AutoRelay || saved.NodeID != 9 {
		t.Fatalf("saved config = %+v, want controller=%s autoRelay=true nodeId=9", saved, newCtrl.server.URL)
	}
	if !waitFor(t, 3*time.Second, func() bool {
		oldCtrl.mu.Lock()
		defer oldCtrl.mu.Unlock()
		return oldCtrl.unpairHits == 1 && oldCtrl.unpairID == testClientID
	}) {
		t.Fatal("expected best-effort unpair call to retired controller during migration")
	}
	select {
	case unexpected := <-oldCtrl.conns:
		_ = unexpected.Close()
		t.Fatal("migrated auto-relay must not reconnect to retired controller")
	default:
	}
}

func TestStartDoesNotMigrateManualPairingOnRetiredURL(t *testing.T) {
	oldCtrl := newFakeController(t)
	newCtrl := newFakeController(t)
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	store := NewStore(configPath)
	if err := store.Save(Config{
		ControllerURL: oldCtrl.server.URL,
		NodeID:        7,
		ClientID:      testClientID,
		Token:         testToken,
		Name:          "ManualPanel",
		AutoRelay:     false,
	}); err != nil {
		t.Fatal(err)
	}

	m := NewManager(Options{
		Handler:  http.NotFoundHandler(),
		Version:  "test",
		Hostname: func() string { return "mac.local" },
	})
	m.Configure(store, 5410)
	m.SetDefaultRelayURL(newCtrl.server.URL)
	m.SetRetiredAutoRelayURLs([]string{oldCtrl.server.URL})
	t.Cleanup(m.Stop)

	m.Start()
	conn := <-oldCtrl.conns
	t.Cleanup(func() { _ = conn.Close() })

	if !waitFor(t, 5*time.Second, func() bool { return m.Status().State == StateOnline }) {
		t.Fatalf("state = %s, want online on manual controller", m.Status().State)
	}
	if got := m.Status().ControllerURL; got != oldCtrl.server.URL {
		t.Fatalf("ControllerURL = %q, want manual controller %q", got, oldCtrl.server.URL)
	}
	select {
	case unexpected := <-newCtrl.conns:
		_ = unexpected.Close()
		t.Fatal("manual pairing must never migrate to default relay")
	default:
	}
}

func TestStartDoesNotMigrateNonRetiredAutoRelay(t *testing.T) {
	customCtrl := newFakeController(t)
	newCtrl := newFakeController(t)
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	store := NewStore(configPath)
	if err := store.Save(Config{
		ControllerURL: customCtrl.server.URL,
		NodeID:        11,
		ClientID:      testClientID,
		Token:         testToken,
		Name:          "CustomAuto",
		AutoRelay:     true,
	}); err != nil {
		t.Fatal(err)
	}

	m := NewManager(Options{
		Handler:  http.NotFoundHandler(),
		Version:  "test",
		Hostname: func() string { return "mac.local" },
	})
	m.Configure(store, 5410)
	m.SetDefaultRelayURL(newCtrl.server.URL)
	m.SetRetiredAutoRelayURLs([]string{"https://retired.example.test"})
	t.Cleanup(m.Stop)

	m.Start()
	conn := <-customCtrl.conns
	t.Cleanup(func() { _ = conn.Close() })

	if !waitFor(t, 5*time.Second, func() bool { return m.Status().State == StateOnline }) {
		t.Fatalf("state = %s, want online on custom controller", m.Status().State)
	}
	if got := m.Status().ControllerURL; got != customCtrl.server.URL {
		t.Fatalf("ControllerURL = %q, want %q", got, customCtrl.server.URL)
	}
}

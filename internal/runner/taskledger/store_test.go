package taskledger

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func unavailableLedgerPath(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, TaskLedgerFile)
}

func TestRequestAdmissionRequiresDurableStorage(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{RequestKindPrompt, RequestKindPermissionReply, RequestKindWorktreeCommit} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			ledger := NewStore(filepath.Join(root, "sessions"))
			goodPath := ledger.Path()
			ledger.SetPathForTest(unavailableLedgerPath(t))
			out := ledger.AdmitRequest(kind, "scope", "session", "request", "digest", nil)
			if out.State != RequestAdmissionUnavailable || out.HTTPStatus != http.StatusServiceUnavailable || out.ErrorCode != ErrCodeRequestStorageUnavailable {
				t.Fatalf("write failure must reject admission: %+v", out)
			}
			if _, found, err := ledger.InspectRequest(kind, "scope", "request", ""); err != nil || found {
				t.Fatalf("failed admission retained a reservation: found=%v err=%v", found, err)
			}
			ledger.SetPathForTest(goodPath)
			if retry := ledger.AdmitRequest(kind, "scope", "session", "request", "digest", nil); retry.State != RequestAdmissionNew {
				t.Fatalf("same identity cannot retry after storage repair: %+v", retry)
			}
			cold := NewStore(filepath.Join(root, "sessions"))
			if rec, found, err := cold.InspectRequest(kind, "scope", "request", "digest"); err != nil || !found || rec.Status != RequestStatusAdmitted {
				t.Fatalf("successful admission was not durable: rec=%+v found=%v err=%v", rec, found, err)
			}
		})
	}
}

func TestUnreadableOrInvalidLedgerFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "invalid json", data: []byte("{not-json")},
		{name: "unsupported version", data: []byte(`{"version":99}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ledgerPath := filepath.Join(root, TaskLedgerFile)
			if err := os.WriteFile(ledgerPath, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			ledger := NewStore(filepath.Join(root, "sessions"))
			out := ledger.AdmitRequest(RequestKindPrompt, "scope", "session", "request", "digest", nil)
			if out.State != RequestAdmissionUnavailable || out.ErrorCode != ErrCodeRequestStorageUnavailable {
				t.Fatalf("ledger load failure must reject admission: %+v", out)
			}
			matches, err := filepath.Glob(ledgerPath + ".*")
			if err != nil || len(matches) != 1 {
				t.Fatalf("expected original ledger to be preserved in a backup, matches=%v err=%v", matches, err)
			}
			if raw, err := os.ReadFile(ledgerPath); err != nil || string(raw) != string(tc.data) {
				t.Fatalf("original ledger was replaced: data=%s err=%v", raw, err)
			}
			cold := NewStore(filepath.Join(root, "sessions"))
			if _, _, err := cold.InspectRequest(RequestKindPrompt, "scope", "request", ""); err == nil {
				t.Fatal("restart incorrectly reported not_admitted for an unreadable ledger")
			}
		})
	}
}

func TestLedgerReadErrorDoesNotOverwriteExistingPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, TaskLedgerFile)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := NewStore(filepath.Join(root, "sessions"))
	if ledger.LoadErr() == nil {
		t.Fatal("directory read was accepted as empty ledger")
	}
	if out := ledger.AdmitRequest(RequestKindWorktreeCommit, "scope", "", "id", "digest", nil); out.State != RequestAdmissionUnavailable {
		t.Fatalf("unexpected admission %+v", out)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("ledger path replaced: %v %v", info, err)
	}
}

func TestCompletionWriteFailureRetainsDurableAdmissionOnRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	storePath := filepath.Join(root, "sessions")
	ledger := NewStore(storePath)
	if adm := ledger.AdmitRequest(RequestKindWorktreeCommit, "scope", "session", "request", "digest", nil); adm.State != RequestAdmissionNew {
		t.Fatalf("admission failed: %+v", adm)
	}
	ledger.SetPathForTest(unavailableLedgerPath(t))
	ledger.CompleteRequest(RequestKindWorktreeCommit, "scope", "session", "request", "digest", http.StatusOK, []byte(`true`), nil)
	cold := NewStore(storePath)
	cold.SettleAdmittedRequestsOnBoot()
	if retry := cold.AdmitRequest(RequestKindWorktreeCommit, "scope", "session", "request", "digest", nil); retry.State != RequestAdmissionUnknown {
		t.Fatalf("lost completion allowed replay after restart: %+v", retry)
	}
}

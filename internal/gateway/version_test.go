package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoxiai/hoxi/internal/buildinfo"
)

func TestGatewayUsesEmbeddedVersionForBootstrapAndUpdates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != buildinfo.Version {
			t.Errorf("update URL version = %q, want %q", r.URL.Query().Get("version"), buildinfo.Version)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"needUpdate":false}`))
	}))
	defer upstream.Close()
	t.Setenv("HOXI_SERVER_URL", upstream.URL)
	g := New(0, "test", t.TempDir(), nil, nil)
	defer g.CloseBrowser()
	w := httptest.NewRecorder()
	g.ServeHTTP(w, localRequest(http.MethodGet, "/api/hoxi/bootstrap", nil))
	var bootstrap struct {
		AppVersion string `json:"appVersion"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap.AppVersion != buildinfo.Version {
		t.Fatalf("bootstrap version = %q, want %q", bootstrap.AppVersion, buildinfo.Version)
	}
	result, err := g.updater.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentVersion != bootstrap.AppVersion {
		t.Fatalf("updater version = %q differs from bootstrap %q", result.CurrentVersion, bootstrap.AppVersion)
	}
}

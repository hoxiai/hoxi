package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIOptionsAfterPositionalsStayLocal(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"browser help", []string{"browser", "--help"}, 0, "Usage:"},
		{"browser short help", []string{"browser", "-h"}, 0, "Usage:"},
		{"browser single dash help", []string{"browser", "-help"}, 0, "Usage:"},
		{"browser help equals", []string{"browser", "--help=true"}, 0, "Usage:"},
		{"unknown command help", []string{"not-a-command", "--help"}, 0, "Usage:"},
		{"unknown command option", []string{"browser", "--not-an-option"}, 2, "flag provided but not defined"},
		{"run help after prompt", []string{"run", "some task", "--help"}, 0, "Usage:"},
		{"chat help after prompt", []string{"chat", "some task", "--help"}, 0, "Usage:"},
		{"help after flag value", []string{"browser", "--model", "test-model", "--help"}, 0, "Usage:"},
		{"em dash help", []string{"browser", "—help"}, 2, "invalid option"},
		{"en dash help", []string{"browser", "–help"}, 2, "invalid option"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "gateway must not be contacted", http.StatusInternalServerError)
			}))
			defer srv.Close()
			booted := false
			boot := func(context.Context, string) (string, func(), error) {
				booted = true
				return "", nil, fmt.Errorf("headless engine must not be started")
			}
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), append([]string{"hoxi"}, tc.args...),
				strings.NewReader(""), &stdout, &stderr, srv.URL, boot)
			if code != tc.code || !strings.Contains(stdout.String()+stderr.String(), tc.want) {
				t.Errorf("exit=%d, stdout=%q, stderr=%q; want exit=%d containing %q",
					code, stdout.String(), stderr.String(), tc.code, tc.want)
			}
			if booted || requests.Load() != 0 {
				t.Errorf("local option handling contacted gateway %d times, booted=%v", requests.Load(), booted)
			}
		})
	}
}

package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// fakeProbe scripts claim/forward results per call.
type fakeProbe struct {
	claims     []bool // result of each claim call; last value repeats
	windowAt   int    // forward call index (0-based) at which the window appears; -1 never
	forwardErr error
	claimCalls int
	forwards   []string
}

func (f *fakeProbe) claim() bool {
	i := f.claimCalls
	f.claimCalls++
	if i >= len(f.claims) {
		i = len(f.claims) - 1
	}
	return f.claims[i]
}

func (f *fakeProbe) forward(payload string) (bool, error) {
	f.forwards = append(f.forwards, payload)
	if f.windowAt >= 0 && len(f.forwards) > f.windowAt {
		return true, f.forwardErr
	}
	return false, nil
}

func TestHandOffLaunch(t *testing.T) {
	sendErr := errors.New("hung")
	cases := []struct {
		name      string
		probe     *fakeProbe
		want      launchOutcome
		wantErr   error
		wantSleep int
	}{
		{name: "first launch becomes primary", probe: &fakeProbe{claims: []bool{true}, windowAt: -1}, want: launchPrimary},
		{name: "running window takes the launch", probe: &fakeProbe{claims: []bool{false}, windowAt: 0}, want: launchForwarded},
		{name: "window of an older build without the claim", probe: &fakeProbe{claims: []bool{true}, windowAt: 0}, want: launchForwarded},
		{name: "waits for a starting instance", probe: &fakeProbe{claims: []bool{false}, windowAt: 3}, want: launchForwarded, wantSleep: 3},
		{name: "takes over when the other exits first", probe: &fakeProbe{claims: []bool{false, false, true}, windowAt: -1}, want: launchPrimary, wantSleep: 2},
		{name: "gives up on an instance that never opens a window", probe: &fakeProbe{claims: []bool{false}, windowAt: -1}, want: launchAbandoned, wantSleep: 10},
		{name: "send failure still means the instance is running", probe: &fakeProbe{claims: []bool{false}, windowAt: 0, forwardErr: sendErr}, want: launchForwarded, wantErr: sendErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sleeps := 0
			got, err := handOffLaunch(tc.probe, "payload", time.Second, 100*time.Millisecond, func(d time.Duration) {
				if d != 100*time.Millisecond {
					t.Fatalf("sleep %v", d)
				}
				sleeps++
			})
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Fatalf("outcome = %v, %v; want %v, %v", got, err, tc.want, tc.wantErr)
			}
			if sleeps != tc.wantSleep {
				t.Fatalf("slept %d times, want %d", sleeps, tc.wantSleep)
			}
			for _, p := range tc.probe.forwards {
				if p != "payload" {
					t.Fatalf("forwarded %q", p)
				}
			}
		})
	}
}

func TestSecondInstancePayloadReachesOnSecondInstance(t *testing.T) {
	args := []string{"hoxi://session/ses_abc123?message=msg_456"}
	payload, err := secondInstancePayload(args, `C:\Users\me`)
	if err != nil {
		t.Fatal(err)
	}
	// Decode exactly as Wails' WM_COPYDATA handler does.
	var data options.SecondInstanceData
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		t.Fatalf("payload %q: %v", payload, err)
	}
	if strings.Join(data.Args, " ") != args[0] || data.WorkingDirectory != `C:\Users\me` {
		t.Fatalf("decoded %+v", data)
	}

	gw := gateway.New(0, "test", t.TempDir(), nil, nil)
	app := &DesktopApp{gateway: gw}
	app.onSecondInstance(data)
	intent, ok := gw.ConsumeDesktopIntent()
	if !ok || intent.SessionID != "ses_abc123" || intent.MessageID != "msg_456" {
		t.Fatalf("intent = %+v (ok=%v)", intent, ok)
	}
}

func TestInstanceNamesPerVariant(t *testing.T) {
	release := ResolveIdentity("release", "")
	dev := ResolveIdentity("dev", "")
	if launchClaimName(release.BundleID) == launchClaimName(dev.BundleID) {
		t.Fatal("release and dev must not share a launch claim")
	}
	class, window := wailsInstanceNames(release.BundleID)
	if class != "wails-app-ai.hoxi.desktop-sic" || window != "wails-app-ai.hoxi.desktop-siw" {
		t.Fatalf("names = %q, %q", class, window)
	}
}

// The probe copies Wails' private window names and message ID; fail loudly
// when a Wails upgrade changes them.
func TestWailsSingleInstanceContractUnchanged(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/wailsapp/wails/v2").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "internal", "frontend", "desktop", "windows", "single_instance.go"))
	if err != nil {
		t.Skipf("wails source unavailable: %v", err)
	}
	for _, want := range []string{
		`id := "wails-app-" + uniqueId`,
		`className := id + "-sic"`,
		`windowName := id + "-siw"`,
		`WMCOPYDATA_SINGLE_INSTANCE_DATA = 1542`,
		`HWND_MESSAGE`,
		`json.Unmarshal([]byte(serialized), &secondInstanceData)`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("wails single_instance.go no longer contains %q; update single_instance*.go", want)
		}
	}
}

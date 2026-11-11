package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionStoreContextInjectionAndNativeCLIDedup(t *testing.T) {
	tmp := t.TempDir()
	svc := NewService(tmp, nil, nil, filepath.Join(tmp, "runner-sessions.json"))
	t.Cleanup(svc.CloseAll)

	currentBlock := "<hoxi_work_store_profile>\nBound Store: AnkerHome US (ID: amz-us-main, Platform: amazon, Marketplace: US, Currency: USD)\n</hoxi_work_store_profile>"
	svc.SetSessionContextResolver(func(sessionID, _ string) string {
		if sessionID == "ses_bound_1" {
			return "  " + currentBlock + "\n"
		}
		return ""
	})

	if got := svc.resolveSessionContextBlock("ses_unbound", tmp); got != "" {
		t.Fatalf("expected empty context block for unbound session, got %q", got)
	}
	resolved := svc.resolveSessionContextBlock("ses_bound_1", tmp)
	if resolved != currentBlock {
		t.Fatalf("unexpected resolved context block: %q", resolved)
	}

	buildTurnSystems := func(userSystem string) (callerSys, fullSys string) {
		callerSys = strings.TrimSpace(userSystem)
		if storeCtx := svc.resolveSessionContextBlock("ses_bound_1", tmp); storeCtx != "" && !strings.Contains(callerSys, "<hoxi_work_store_profile>") {
			if callerSys == "" {
				callerSys = storeCtx
			} else {
				callerSys = storeCtx + "\n\n" + callerSys
			}
		}
		fullSys = strings.TrimSpace(commandExecutionGuidance + "\n\n" + callerSys)
		return callerSys, fullSys
	}

	rec := SessionRecord{
		ID:         "ses_bound_1",
		Runtime:    string(RuntimeClaudeCode),
		ProjectDir: tmp,
	}

	// Turn 1 (fresh CLI conversation): delivers commandExecutionGuidance + store profile + user system.
	caller1, full1 := buildTurnSystems("Follow brand voice.")
	sys1 := svc.nativeCLISystemForTurn(rec, caller1, full1)
	if !strings.Contains(sys1, "hoxi ecom") || !strings.Contains(sys1, "amz-us-main") || !strings.Contains(sys1, "Follow brand voice.") {
		t.Fatalf("expected Turn 1 system to include ecom guidance, store profile, and caller system, got:\n%s", sys1)
	}
	svc.recordCLISystemDelivered(rec.ID, rec.Runtime, caller1)

	// Turn 2 (resumed CLI conversation with unchanged store binding): deduplicated to empty string.
	rec.ExternalID = "cld_conv_1"
	caller2, full2 := buildTurnSystems("Follow brand voice.")
	if sys2 := svc.nativeCLISystemForTurn(rec, caller2, full2); sys2 != "" {
		t.Fatalf("expected unchanged resumed Turn 2 system to be deduplicated, got:\n%s", sys2)
	}

	// Turn 3 (resumed CLI conversation after store switch / updated economics): re-delivers updated callerSystem.
	currentBlock = "<hoxi_work_store_profile>\nBound Store: AnkerHome UK (ID: amz-uk-2, Platform: amazon, Marketplace: UK, Currency: GBP)\n</hoxi_work_store_profile>"
	caller3, full3 := buildTurnSystems("Follow brand voice.")
	sys3 := svc.nativeCLISystemForTurn(rec, caller3, full3)
	if !strings.Contains(sys3, "amz-uk-2") {
		t.Fatalf("expected updated store profile on Turn 3 after binding change, got:\n%s", sys3)
	}
}

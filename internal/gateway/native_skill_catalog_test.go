package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/skills"
)

func TestBuildNativeSkillCatalogPromptDeterministicSortAndExposure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hoxi/skills/list", func(w http.ResponseWriter, _ *http.Request) {
		resp := skills.ListResponse{
			Skills: []skills.SkillDescriptor{
				{Name: "zebra-deploy", Description: "Deploy zebra service", Exposure: skills.ExposureAuto},
				{Name: "hidden-blocked", Description: "Should never appear", Exposure: skills.ExposureUnavailable},
				{Name: "alpha-audit", Description: "Audit repository boundaries", Exposure: skills.ExposureAuto},
				{Name: "zeta-manual", Description: "Manual skill", Exposure: skills.ExposureOnDemand},
				{Name: "beta-manual", Description: "Beta manual skill", Exposure: skills.ExposureOnDemand},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	gw := &Gateway{mux: mux, rootDir: t.TempDir()}
	out := gw.buildNativeSkillCatalogPrompt(context.Background(), "ses_test_sort", gw.rootDir)
	if out == "" {
		t.Fatal("expected non-empty skill catalog prompt")
	}
	if strings.Contains(out, "hidden-blocked") {
		t.Fatalf("unavailable skill must be excluded, got:\n%s", out)
	}
	alphaIdx := strings.Index(out, "<name>alpha-audit</name>")
	zebraIdx := strings.Index(out, "<name>zebra-deploy</name>")
	if alphaIdx < 0 || zebraIdx < 0 || alphaIdx >= zebraIdx {
		t.Fatalf("expected alpha-audit before zebra-deploy in sorted catalog, got:\n%s", out)
	}
	if !strings.Contains(out, "On-demand skills, listed by name only: beta-manual, zeta-manual.") {
		t.Fatalf("expected deterministically sorted on-demand skills line, got:\n%s", out)
	}
}

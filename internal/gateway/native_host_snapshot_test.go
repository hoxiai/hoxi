package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func TestBrowserSnapshotOmitsSelectorsFromModelOutputOnly(t *testing.T) {
	longSel := "body > div:nth-of-type(3) > main > section:nth-of-type(2) > form > button:nth-of-type(4)"
	raw := `{"url":"https://example.com","snapshot":{"text":"Checkout","controls":[` +
		`{"ref":"@e1","selector":"` + longSel + `","tag":"button","text":"Pay"},` +
		`{"selector":"#orphan","tag":"a","text":"No ref"}]}}`
	res, err := extractHostActionResult("hoxi_browser", http.StatusOK, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, longSel) {
		t.Fatalf("model output must not carry selectors for controls that have refs: %s", res.Output)
	}
	for _, want := range []string{`"ref":"@e1"`, `"text":"Pay"`, `"text":"Checkout"`, `"selector":"#orphan"`} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("model output lost %s: %s", want, res.Output)
		}
	}
	snap, _ := res.Metadata["snapshot"].(map[string]any)
	controls, _ := snap["controls"].([]any)
	first, _ := controls[0].(map[string]any)
	if first["selector"] != longSel {
		t.Fatalf("metadata must keep the original selector, got %v", first["selector"])
	}
}

func TestComputerResultIsNotSnapshotCompacted(t *testing.T) {
	raw := `{"snapshot":{"controls":[{"ref":"@e1","selector":"#keep"}]}}`
	res, err := extractHostActionResult("hoxi_computer", http.StatusOK, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, `"selector":"#keep"`) {
		t.Fatalf("non-browser tools must be passed through unchanged: %s", res.Output)
	}
}

package runner

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeTrimmedPayload(t *testing.T, line string) map[string]interface{} {
	t.Helper()
	payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimRight(line, "\r\n"), "data:"))
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	var root map[string]interface{}
	if err := dec.Decode(&root); err != nil {
		t.Fatalf("decode trimmed payload: %v", err)
	}
	return root
}

func TestTrimSummaryDiffPatchesDropsOnlyPatchBodies(t *testing.T) {
	t.Parallel()
	line := `data: {"type":"message.updated","properties":{"info":{"id":"msg_1","time":{"created":1792489905921},"summary":{"diffs":[{"file":"web/a.ts","patch":"@@ -1 +1 @@","additions":3,"deletions":1},{"file":"web/b.ts","patch":"long diff"}]}}}}` + "\n"

	got := trimSummaryDiffPatches(line)
	if strings.Contains(got, `"patch"`) {
		t.Fatalf("patch bodies survived trimming: %s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatal("trimming dropped the frame terminator")
	}
	root := decodeTrimmedPayload(t, got)
	info := root["properties"].(map[string]interface{})["info"].(map[string]interface{})
	summary := info["summary"].(map[string]interface{})
	diffs := summary["diffs"].([]interface{})
	if len(diffs) != 2 {
		t.Fatalf("expected both diff entries to stay, got %d", len(diffs))
	}
	first := diffs[0].(map[string]interface{})
	if first["file"] != "web/a.ts" || first["additions"] != json.Number("3") || first["deletions"] != json.Number("1") {
		t.Fatalf("diff metadata changed: %+v", first)
	}
	if info["time"].(map[string]interface{})["created"] != json.Number("1792489905921") {
		t.Fatalf("numeric fields lost precision: %+v", info["time"])
	}
}

func TestTrimSummaryDiffPatchesHandlesGlobalEventEnvelope(t *testing.T) {
	t.Parallel()
	line := `data: {"directory":"/tmp/ws","payload":{"type":"message.updated","properties":{"info":{"id":"msg_2","summary":{"diffs":[{"file":"a.go","patch":"xxx"}]}}}}}` + "\n"
	got := trimSummaryDiffPatches(line)
	if strings.Contains(got, `"patch"`) {
		t.Fatalf("wrapped payload kept patch bodies: %s", got)
	}
	root := decodeTrimmedPayload(t, got)
	payload := root["payload"].(map[string]interface{})
	info := payload["properties"].(map[string]interface{})["info"].(map[string]interface{})
	if info["summary"].(map[string]interface{})["diffs"].([]interface{})[0].(map[string]interface{})["file"] != "a.go" {
		t.Fatalf("wrapped payload metadata lost: %+v", root)
	}
}

func TestTrimSummaryDiffPatchesLeavesOtherFramesByteIdentical(t *testing.T) {
	t.Parallel()
	frames := []string{
		`data: {"type":"message.part.delta","properties":{"sessionID":"ses_1","delta":"hi"}}` + "\n",
		`data: {"type":"message.updated","properties":{"info":{"id":"msg_3"}}}` + "\n",
		"data: not-json\n",
		": keepalive\n",
	}
	for _, frame := range frames {
		if got := trimSummaryDiffPatches(frame); got != frame {
			t.Fatalf("frame was rewritten unexpectedly:\nwant %q\ngot  %q", frame, got)
		}
	}
}

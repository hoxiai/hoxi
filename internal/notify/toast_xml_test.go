package notify

import (
	"encoding/xml"
	"testing"
)

func TestWindowsToastXMLPreservesTextAndOnlyActivatesSessionLinks(t *testing.T) {
	for _, tc := range []struct {
		name, id, launch string
		registered       bool
	}{
		{"registered", "ses_abc-123", "hoxi://session/ses_abc-123", true},
		{"generic", "", "", true},
		{"no protocol handler", "ses_abc-123", "", false},
		{"not navigation", `ses_abc?execute=1`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Title: `Test <toast>`, Body: `中文 & "quoted"`, SessionID: tc.id}
			payload, err := windowsToastXML(req, tc.registered)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Kind   string   `xml:"activationType,attr"`
				Launch string   `xml:"launch,attr"`
				Text   []string `xml:"visual>binding>text"`
			}
			if err := xml.Unmarshal([]byte(payload), &got); err != nil {
				t.Fatal(err)
			}
			if got.Launch != tc.launch || (got.Kind == "protocol") != (tc.launch != "") {
				t.Fatalf("unexpected toast activation: %s", payload)
			}
			if len(got.Text) != 2 || got.Text[0] != req.Title || got.Text[1] != req.Body {
				t.Fatalf("unexpected toast text: %s", payload)
			}
		})
	}
}

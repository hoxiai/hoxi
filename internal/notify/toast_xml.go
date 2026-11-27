package notify

import (
	"encoding/xml"
	"regexp"
)

var toastSessionID = regexp.MustCompile(`^ses_[a-zA-Z0-9_-]{1,92}$`)

// The activation can only navigate to an existing Hoxi session. No command,
// path or approval decision is encoded in the notification.
func windowsToastXML(req Request, canOpenSession bool) (string, error) {
	type binding struct {
		Template string   `xml:"template,attr"`
		Text     []string `xml:"text"`
	}
	toast := struct {
		XMLName        xml.Name `xml:"toast"`
		ActivationType string   `xml:"activationType,attr,omitempty"`
		Launch         string   `xml:"launch,attr,omitempty"`
		Binding        binding  `xml:"visual>binding"`
	}{Binding: binding{Template: "ToastGeneric", Text: []string{req.Title, req.Body}}}
	if canOpenSession && toastSessionID.MatchString(req.SessionID) {
		toast.ActivationType = "protocol"
		toast.Launch = "hoxi://session/" + req.SessionID
	}
	data, err := xml.Marshal(toast)
	return string(data), err
}

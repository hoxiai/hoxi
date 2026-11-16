package tunnel

import (
	"encoding/base64"
	"encoding/json"

	qrcode "github.com/skip2/go-qrcode"
)

// relayQRImageSize is the PNG edge length in pixels, matching the LAN pairing
// QR so both scan reliably from a phone held 15-30cm from the screen.
const relayQRImageSize = 320

// RelayQR is the desktop-side QR the Hoxi mobile app scans to add this desktop
// as a relay node through the GoPanel it is paired with. The payload carries
// only the panel address and node identity: the phone still uses its own
// GoPanel login, so no credential is ever embedded in the code.
type RelayQR struct {
	Available bool   `json:"available"`
	Panel     string `json:"panel,omitempty"`
	NodeID    uint   `json:"nodeId,omitempty"`
	Name      string `json:"name,omitempty"`
	Payload   string `json:"payload,omitempty"`
	QRPng     string `json:"qrPng,omitempty"`
}

// RelayQRInfo builds the current relay QR from the tunnel status. It is empty
// (Available=false) until the desktop is paired and has a node id.
func (m *Manager) RelayQRInfo() RelayQR {
	m.mu.Lock()
	enabled := m.status.Enabled
	nodeID := m.status.NodeID
	panel := m.status.ControllerURL
	name := m.status.Name
	m.mu.Unlock()

	if !enabled || nodeID == 0 || panel == "" {
		return RelayQR{}
	}
	payload, err := json.Marshal(map[string]any{
		"type":   "gopanel-relay",
		"panel":  panel,
		"nodeId": nodeID,
		"name":   name,
	})
	if err != nil {
		return RelayQR{}
	}
	png, err := qrcode.Encode(string(payload), qrcode.Medium, relayQRImageSize)
	if err != nil {
		return RelayQR{}
	}
	return RelayQR{
		Available: true,
		Panel:     panel,
		NodeID:    nodeID,
		Name:      name,
		Payload:   string(payload),
		QRPng:     "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	}
}

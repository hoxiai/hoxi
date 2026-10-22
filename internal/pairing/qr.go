package pairing

import (
	"encoding/base64"

	qrcode "github.com/skip2/go-qrcode"
)

// qrImageSize is the PNG edge length in pixels. 320 scans reliably from a
// phone held 15-30cm from the screen while staying small enough to inline.
const qrImageSize = 320

// encodeQR renders content as a base64 PNG QR code.
func encodeQR(content string) (string, error) {
	png, err := qrcode.Encode(content, qrcode.Medium, qrImageSize)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(png), nil
}

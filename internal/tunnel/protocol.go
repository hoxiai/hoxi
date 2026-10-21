// Package tunnel is Hoxi's client for the GoPanel reverse tunnel. Hoxi dials
// out to a paired public GoPanel, registers its gateway service, and serves
// requests that GoPanel multiplexes over the WebSocket. Requests are handed to
// an in-process http.Handler, so remote control never opens a listening port.
//
// The wire format must stay byte-compatible with
// gopanel_v2/app/service/tunnel_protocol.go.
package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"
)

const (
	maxFrameSize    = 4 << 20
	maxDataSize     = maxFrameSize - 10
	chunkSize       = 32 << 10
	heartbeatPeriod = 20 * time.Second
	readTimeout     = 65 * time.Second
	writeTimeout    = 15 * time.Second
)

const (
	frameOpen byte = iota + 1
	frameResponse
	frameData
	frameEnd
	frameReset
	framePing
	framePong
	frameServiceSync
)

const (
	dataHTTP byte = iota
	dataWebSocketText
	dataWebSocketBinary
)

// ServicePublicID is the service GoPanel routes mobile Hoxi requests to.
const ServicePublicID = "hoxi-gateway"

type frame struct {
	Type     byte
	StreamID uint64
	Payload  []byte
}

type openFrame struct {
	ServiceID  string              `json:"serviceId"`
	Method     string              `json:"method"`
	RequestURI string              `json:"requestUri"`
	Headers    map[string][]string `json:"headers"`
	HasBody    bool                `json:"hasBody"`
	Upgrade    bool                `json:"upgrade"`
}

type responseFrame struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
}

type serviceSync struct {
	Hostname string          `json:"hostname"`
	Version  string          `json:"version"`
	Services []serviceRecord `json:"services"`
}

type serviceRecord struct {
	PublicID   string `json:"publicId"`
	Name       string `json:"name"`
	Scheme     string `json:"scheme"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
	Enabled    bool   `json:"enabled"`
}

func encodeFrame(f frame) []byte {
	data := make([]byte, 9+len(f.Payload))
	data[0] = f.Type
	binary.BigEndian.PutUint64(data[1:9], f.StreamID)
	copy(data[9:], f.Payload)
	return data
}

func decodeFrame(data []byte) (frame, error) {
	if len(data) < 9 {
		return frame{}, errors.New("tunnel frame is too short")
	}
	return frame{Type: data[0], StreamID: binary.BigEndian.Uint64(data[1:9]), Payload: data[9:]}, nil
}

func jsonFrame(frameType byte, streamID uint64, value any) (frame, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return frame{}, err
	}
	return frame{Type: frameType, StreamID: streamID, Payload: payload}, nil
}

func encodeData(kind byte, payload []byte) []byte {
	result := make([]byte, 1+len(payload))
	result[0] = kind
	copy(result[1:], payload)
	return result
}

func decodeData(payload []byte) (byte, []byte, error) {
	if len(payload) == 0 {
		return 0, nil, errors.New("tunnel data payload is empty")
	}
	return payload[0], payload[1:], nil
}

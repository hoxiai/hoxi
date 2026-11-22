package appbridge

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// RuntimeEndpoint is the non-secret discovery document for a local host.
type RuntimeEndpoint struct {
	Protocol  string `json:"protocol"`
	BaseURL   string `json:"baseURL"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"startedAt"`
}

func WriteRuntimeEndpoint(endpoint RuntimeEndpoint) error {
	u, err := url.Parse(endpoint.BaseURL)
	if err != nil || u.Scheme != "http" || u.User != nil || endpoint.BaseURL != "http://"+u.Host || endpoint.PID <= 0 {
		return errors.New("invalid local runtime endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return errors.New("runtime endpoint must use a loopback IP and port")
	}
	endpoint.Protocol = ProtocolVersion
	if endpoint.StartedAt == 0 {
		endpoint.StartedAt = time.Now().UnixMilli()
	}
	path := hoxipaths.RuntimeEndpointPath()
	return hoxipaths.WithStateFileLock(path, func() error {
		return hoxipaths.WriteJSONAtomic(path, endpoint, 0o600)
	})
}

func ClearRuntimeEndpoint(pid int) error {
	if pid <= 0 {
		return errors.New("invalid runtime endpoint PID")
	}
	path := hoxipaths.RuntimeEndpointPath()
	return hoxipaths.WithStateFileLock(path, func() error {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var endpoint RuntimeEndpoint
		if err := json.Unmarshal(raw, &endpoint); err != nil {
			return err
		}
		if endpoint.PID != pid {
			return nil
		}
		return os.Remove(path)
	})
}

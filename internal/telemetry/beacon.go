// Package telemetry 上报匿名客户端使用事件到 hoxi.ai，用于安装量与活跃度统计。
// 上报为 fire-and-forget：任何失败都静默处理，绝不影响客户端启动与运行。
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/auth"
	"github.com/hoxiai/hoxi/internal/buildinfo"
)

const (
	installIDFile  = "device-id"
	requestTimeout = 3 * time.Second
)

// Event 与服务端 /api/hoxi/client-events 的请求体一一对应。
type Event struct {
	Event     string `json:"event"`
	InstallID string `json:"installId"`
	Package   string `json:"package"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Version   string `json:"version"`
}

// LoadOrCreateInstallID 返回本地匿名安装 ID（~/.hoxi/device-id，随机 UUID，无个人信息）。
// created=true 表示本次是全新安装（或本地 ID 缺失/损坏后重新生成）。
func LoadOrCreateInstallID() (id string, created bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	path := filepath.Join(home, ".hoxi", installIDFile)

	if data, readErr := os.ReadFile(path); readErr == nil {
		existing := strings.TrimSpace(string(data))
		if len(existing) >= 8 && len(existing) <= 128 {
			return existing, false, nil
		}
	}

	id = newInstallID()
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return id, true, mkErr
	}
	if writeErr := os.WriteFile(path, []byte(id), 0o600); writeErr != nil {
		return id, true, writeErr
	}
	return id, true, nil
}

func newInstallID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// ReportLaunch 上报一次启动事件。首次启动（新 installId）上报 event=install，其余为 launch。
// 已登录设备会附带设备令牌，便于服务端关联到用户；上报失败仅记日志。
func ReportLaunch(logger *slog.Logger) {
	go func() {
		id, created, err := LoadOrCreateInstallID()
		if err != nil && logger != nil {
			logger.Warn("telemetry install id persistence failed", "error", err)
		}

		eventName := "launch"
		if created {
			eventName = "install"
		}

		serverURL := strings.TrimRight(os.Getenv("HOXI_SERVER_URL"), "/")
		if serverURL == "" {
			serverURL = "https://hoxi.ai"
		}

		osName := runtime.GOOS
		if osName == "darwin" {
			osName = "mac"
		}
		archName := runtime.GOARCH
		if archName == "amd64" {
			archName = "x64"
		}

		payload, _ := json.Marshal(Event{
			Event:     eventName,
			InstallID: id,
			Package:   "desktop",
			OS:        osName,
			Arch:      archName,
			Version:   buildinfo.Version,
		})

		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/hoxi/client-events", bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "HoxiDesktop/"+buildinfo.Version)
		if stored, loadErr := auth.NewTokenStore().Load(); loadErr == nil && stored != nil && stored.DeviceToken != "" {
			req.Header.Set("Authorization", "Bearer "+stored.DeviceToken)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if logger != nil {
				logger.Warn("telemetry report failed", "error", err)
			}
			return
		}
		_ = resp.Body.Close()
	}()
}

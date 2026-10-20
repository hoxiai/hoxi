package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

type systemRuntimeInfoResponse struct {
	AppVersion          string            `json:"appVersion"`
	OpenCodeBinPath     string            `json:"openCodeBinPath"`
	OpenCodeVersion     string            `json:"openCodeVersion"`
	OpenCodePort        int               `json:"openCodePort"`
	WSBaseURL           string            `json:"wsBaseURL"`
	RootDir             string            `json:"rootDir"`
	UserConfigPath      string            `json:"userConfigPath"`
	WorkspaceConfigPath string            `json:"workspaceConfigPath"`
	OS                  string            `json:"os"`
	Arch                string            `json:"arch"`
	GoVersion           string            `json:"goVersion"`
	SystemUsername      string            `json:"systemUsername"`
	GitUserName         string            `json:"gitUserName"`
	GitUserEmail        string            `json:"gitUserEmail"`
	Status              statusbar.Summary `json:"status"`
}

var (
	cachedUserIdentityOnce sync.Once
	cachedSystemUsername   string
	cachedGitUserName      string
	cachedGitUserEmail     string
)

func resolveLocalUserIdentity() (sysUser, gitName, gitEmail string) {
	cachedUserIdentityOnce.Do(func() {
		if u, err := user.Current(); err == nil && u != nil {
			uname := strings.TrimSpace(u.Username)
			if idx := strings.LastIndex(uname, `\`); idx >= 0 && idx < len(uname)-1 {
				uname = uname[idx+1:]
			}
			cachedSystemUsername = uname
		}
		readGitGlobal := func(key string) string {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "git", "config", "--global", key)
			cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
			out, err := cmd.Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}
		cachedGitUserName = readGitGlobal("user.name")
		cachedGitUserEmail = readGitGlobal("user.email")
	})
	return cachedSystemUsername, cachedGitUserName, cachedGitUserEmail
}

func (g *Gateway) handleSystemRuntimeInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	binPath := opencode.FindBinary("opencode")
	ver := opencode.ResolveVersion(binPath)
	sysUser, gitName, gitEmail := resolveLocalUserIdentity()

	var st statusbar.Summary
	if g.status != nil {
		st = g.status.Snapshot()
	}

	var wsConfigPath string
	if g.providers != nil {
		wsConfigPath = g.providers.WorkspaceHoxiModelsPath()
	}

	writeJSON(w, http.StatusOK, systemRuntimeInfoResponse{
		AppVersion:          buildinfo.Version,
		OpenCodeBinPath:     binPath,
		OpenCodeVersion:     ver,
		OpenCodePort:        g.openCodePort,
		WSBaseURL:           g.wsBaseURL,
		RootDir:             g.rootDir,
		UserConfigPath:      provider.UserHoxiModelsPath(),
		WorkspaceConfigPath: wsConfigPath,
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		GoVersion:           runtime.Version(),
		SystemUsername:      sysUser,
		GitUserName:         gitName,
		GitUserEmail:        gitEmail,
		Status:              st,
	})
}

type clipboardImageItem struct {
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	DataURL  string `json:"dataUrl"`
}

const maxClipboardImageBytes = 15 * 1024 * 1024

const macPasteboardJXA = `
ObjC.import("AppKit");
const pb = $.NSPasteboard.generalPasteboard;
const classes = $.NSArray.arrayWithObject($.NSURL.class);
const opts = $.NSDictionary.dictionaryWithObjectForKey(
  $.NSNumber.numberWithBool(true),
  $.NSPasteboardURLReadingFileURLsOnlyKey
);
const urls = pb.readObjectsForClassesOptions(classes, opts);
const files = [];
if (urls && urls.count > 0) {
  for (let i = 0; i < urls.count; i++) {
    const p = ObjC.unwrap(urls.objectAtIndex(i).path);
    if (p) files.push(p);
  }
}
let rawPngBase64 = "";
if (files.length === 0) {
  let pngData = pb.dataForType("public.png");
  if (!pngData || pngData.length === 0) {
    const img = $.NSImage.alloc.initWithPasteboard(pb);
    if (img && img.isValid) {
      const tiff = img.TIFFRepresentation;
      if (tiff) {
        const rep = $.NSBitmapImageRep.imageRepWithData(tiff);
        if (rep) {
          pngData = rep.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $.NSDictionary.dictionary);
        }
      }
    }
  }
  if (pngData && pngData.length > 0 && pngData.length <= 15728640) {
    rawPngBase64 = ObjC.unwrap(pngData.base64EncodedStringWithOptions(0));
  }
}
JSON.stringify({ files, rawPngBase64 });
`

func readClipboardImageFile(path string) (clipboardImageItem, bool) {
	baseName := filepath.Base(path)
	kind, directMime, err := attachment.ClassifyFile(baseName, "")
	if err != nil || kind == "" {
		return clipboardImageItem{}, false
	}
	ext := strings.ToLower(filepath.Ext(path))
	needsConvert := false
	switch ext {
	case ".bmp", ".tif", ".tiff", ".heic", ".heif", ".avif":
		needsConvert = true
	}

	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > maxClipboardImageBytes*2 {
		return clipboardImageItem{}, false
	}

	if needsConvert && runtime.GOOS == "darwin" {
		tmpFile, err := os.CreateTemp("", "hoxi-clip-*.png")
		if err != nil {
			return clipboardImageItem{}, false
		}
		tmpPath := tmpFile.Name()
		_ = tmpFile.Close()
		defer os.Remove(tmpPath)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sips", "-s", "format", "png", path, "--out", tmpPath)
		if cmd.Run() != nil {
			return clipboardImageItem{}, false
		}
		raw, err := os.ReadFile(tmpPath)
		if err != nil || len(raw) == 0 || len(raw) > maxClipboardImageBytes {
			return clipboardImageItem{}, false
		}
		stem := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		return clipboardImageItem{
			Filename: stem + ".png",
			Mime:     "image/png",
			DataURL:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw),
		}, true
	}

	if directMime == "" {
		return clipboardImageItem{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > maxClipboardImageBytes {
		return clipboardImageItem{}, false
	}
	return clipboardImageItem{
		Filename: baseName,
		Mime:     directMime,
		DataURL:  "data:" + directMime + ";base64," + base64.StdEncoding.EncodeToString(raw),
	}, true
}

func (g *Gateway) handleClipboardImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	filesOnly := r.URL.Query().Get("filesOnly") == "1"
	if runtime.GOOS != "darwin" {
		writeJSON(w, http.StatusOK, map[string]any{"images": []clipboardImageItem{}})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", macPasteboardJXA)
	out, err := cmd.Output()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"images": []clipboardImageItem{}})
		return
	}

	var parsed struct {
		Files        []string `json:"files"`
		RawPngBase64 string   `json:"rawPngBase64"`
	}
	if json.Unmarshal(out, &parsed) != nil {
		writeJSON(w, http.StatusOK, map[string]any{"images": []clipboardImageItem{}})
		return
	}

	images := make([]clipboardImageItem, 0, 4)
	for _, f := range parsed.Files {
		if len(images) >= 4 {
			break
		}
		if item, ok := readClipboardImageFile(f); ok {
			images = append(images, item)
		}
	}
	if len(images) == 0 && !filesOnly && parsed.RawPngBase64 != "" {
		images = append(images, clipboardImageItem{
			Filename: "clipboard-image.png",
			Mime:     "image/png",
			DataURL:  "data:image/png;base64," + parsed.RawPngBase64,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": images})
}

func (g *Gateway) handleKeepAwake(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var snap statusbar.Summary
		if g.status != nil {
			snap = g.status.Snapshot()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":   snap.KeepAwakeEnabled,
			"active":    snap.KeepAwakeActive,
			"supported": statusbar.KeepAwakeSupported(),
		})
	case http.MethodPost, http.MethodPut:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if g.status != nil {
			g.status.SetKeepAwakeEnabled(body.Enabled)
		}
		var snap statusbar.Summary
		if g.status != nil {
			snap = g.status.Snapshot()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"enabled":   snap.KeepAwakeEnabled,
			"active":    snap.KeepAwakeActive,
			"supported": statusbar.KeepAwakeSupported(),
		})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

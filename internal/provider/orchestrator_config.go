package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const OrchestratorProviderID = "hoxi-orchestrator"

// ErrInvalidOrchestratorConfig signals a malformed Orchestrator settings update.
var ErrInvalidOrchestratorConfig = errors.New("invalid orchestrator config")

// OrchestratorConfig persists the user's default Orchestrator (planner/reviewer)
// and Executor (search/edit/build/test) model pair in ~/.hoxi/orchestrator.json.
type OrchestratorConfig struct {
	OrchestratorModel  string `json:"orchestratorModel,omitempty"`
	OrchestratorEffort string `json:"orchestratorEffort,omitempty"`
	ExecutorModel      string `json:"executorModel,omitempty"`
	ExecutorEffort     string `json:"executorEffort,omitempty"`
}

func (s *Service) orchestratorConfigFilePath() string {
	if p := hoxipaths.Join("orchestrator.json"); p != "" {
		return p
	}
	if root := strings.TrimSpace(s.rootDir); root != "" {
		return filepath.Join(root, ".hoxi", "orchestrator.json")
	}
	return ""
}

func validateAndNormalizeOrchestratorModelSpec(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: %q must use <providerID>/<modelID> format", ErrInvalidOrchestratorConfig, raw)
	}
	pid := strings.TrimSpace(parts[0])
	mid := strings.TrimSpace(parts[1])
	if pid == "" || mid == "" {
		return "", fmt.Errorf("%w: %q must include non-empty providerID and modelID", ErrInvalidOrchestratorConfig, raw)
	}
	if pid == AutoProviderID || pid == OrchestratorProviderID {
		return "", fmt.Errorf("%w: virtual provider %q cannot be pinned in orchestrator settings", ErrInvalidOrchestratorConfig, pid)
	}
	if strings.ContainsAny(pid, " \t\r\n") || strings.ContainsAny(mid, " \t\r\n") {
		return "", fmt.Errorf("%w: %q must not contain whitespace", ErrInvalidOrchestratorConfig, raw)
	}
	return pid + "/" + mid, nil
}

func validateAndNormalizeOrchestratorEffort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	norm := agentcli.NormalizeEffortName(trimmed)
	if norm == "" {
		return "", fmt.Errorf("%w: unsupported effort %q", ErrInvalidOrchestratorConfig, raw)
	}
	return norm, nil
}

func normalizeOrchestratorConfigBestEffort(cfg OrchestratorConfig) OrchestratorConfig {
	if norm, err := validateAndNormalizeOrchestratorModelSpec(cfg.OrchestratorModel); err == nil {
		cfg.OrchestratorModel = norm
	} else {
		cfg.OrchestratorModel = ""
	}
	if norm, err := validateAndNormalizeOrchestratorEffort(cfg.OrchestratorEffort); err == nil {
		cfg.OrchestratorEffort = norm
	} else {
		cfg.OrchestratorEffort = ""
	}
	if norm, err := validateAndNormalizeOrchestratorModelSpec(cfg.ExecutorModel); err == nil {
		cfg.ExecutorModel = norm
	} else {
		cfg.ExecutorModel = ""
	}
	if norm, err := validateAndNormalizeOrchestratorEffort(cfg.ExecutorEffort); err == nil {
		cfg.ExecutorEffort = norm
	} else {
		cfg.ExecutorEffort = ""
	}
	return cfg
}

// LoadOrchestratorConfig reads ~/.hoxi/orchestrator.json, backing up corrupt files
// instead of silently overwriting them.
func (s *Service) LoadOrchestratorConfig() OrchestratorConfig {
	if s == nil {
		return OrchestratorConfig{}
	}
	s.orchestratorMu.Lock()
	defer s.orchestratorMu.Unlock()
	return s.loadOrchestratorConfigLocked()
}

func (s *Service) loadOrchestratorConfigLocked() OrchestratorConfig {
	path := s.orchestratorConfigFilePath()
	if path == "" {
		return OrchestratorConfig{}
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return OrchestratorConfig{}
	}
	var cfg OrchestratorConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		_ = os.Rename(path, path+".corrupt-"+strconv.FormatInt(time.Now().UnixMilli(), 10))
		return OrchestratorConfig{}
	}
	return normalizeOrchestratorConfigBestEffort(cfg)
}

// UpdateOrchestratorConfig merges non-nil fields into ~/.hoxi/orchestrator.json
// atomically. Unknown fields or invalid model/effort values fail cleanly.
func (s *Service) UpdateOrchestratorConfig(patch map[string]*string) (OrchestratorConfig, error) {
	if s == nil {
		return OrchestratorConfig{}, fmt.Errorf("%w: provider service unavailable", ErrInvalidOrchestratorConfig)
	}
	normalized := make(map[string]string, len(patch))
	for k, v := range patch {
		switch k {
		case "orchestratorModel", "executorModel":
			if v == nil {
				continue
			}
			norm, err := validateAndNormalizeOrchestratorModelSpec(*v)
			if err != nil {
				return OrchestratorConfig{}, err
			}
			normalized[k] = norm
		case "orchestratorEffort", "executorEffort":
			if v == nil {
				continue
			}
			norm, err := validateAndNormalizeOrchestratorEffort(*v)
			if err != nil {
				return OrchestratorConfig{}, err
			}
			normalized[k] = norm
		default:
			return OrchestratorConfig{}, fmt.Errorf("%w: unknown field %q", ErrInvalidOrchestratorConfig, k)
		}
	}

	s.orchestratorMu.Lock()
	defer s.orchestratorMu.Unlock()

	cur := s.loadOrchestratorConfigLocked()
	if v, ok := normalized["orchestratorModel"]; ok {
		cur.OrchestratorModel = v
	}
	if v, ok := normalized["orchestratorEffort"]; ok {
		cur.OrchestratorEffort = v
	}
	if v, ok := normalized["executorModel"]; ok {
		cur.ExecutorModel = v
	}
	if v, ok := normalized["executorEffort"]; ok {
		cur.ExecutorEffort = v
	}

	path := s.orchestratorConfigFilePath()
	if path == "" {
		return cur, nil
	}
	if err := hoxipaths.WriteJSONAtomic(path, cur, 0o600); err != nil {
		return cur, err
	}
	return cur, nil
}

func (s *Service) handleOrchestratorConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.LoadOrchestratorConfig()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"config": cfg,
		})
	case http.MethodPut, http.MethodPatch:
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "failed to read request body"})
			return
		}
		var rawMap map[string]json.RawMessage
		dec := json.NewDecoder(bytes.NewReader(body))
		if err := dec.Decode(&rawMap); err != nil || rawMap == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON object"})
			return
		}
		patch := make(map[string]*string, len(rawMap))
		for k, rawVal := range rawMap {
			trimmedVal := bytes.TrimSpace(rawVal)
			if bytes.Equal(trimmedVal, []byte("null")) {
				empty := ""
				patch[k] = &empty
				continue
			}
			var strVal string
			if err := json.Unmarshal(trimmedVal, &strVal); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("field %q must be a string", k)})
				return
			}
			patch[k] = &strVal
		}
		updated, err := s.UpdateOrchestratorConfig(patch)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrInvalidOrchestratorConfig) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"config": updated,
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET, PUT, or PATCH required"})
	}
}

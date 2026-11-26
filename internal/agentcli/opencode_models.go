package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

var (
	ocCLIModelsMu     sync.Mutex
	ocCLICachedBin    string
	ocCLICachedTime   time.Time
	ocCLICachedList   []ModelItem
	ocCLIProbeTimeout = 8 * time.Second
)

type rawOpenCodeVerboseModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Cost       *struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"cost"`
	Limit *struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Capabilities *struct {
		Reasoning  bool `json:"reasoning"`
		Attachment bool `json:"attachment"`
		Input      *struct {
			Image bool `json:"image"`
		} `json:"input"`
	} `json:"capabilities"`
	Variants map[string]struct {
		Disabled bool `json:"disabled"`
	} `json:"variants"`
}

func formatOpenCodeModelName(slug string) string {
	trimmed := strings.TrimSpace(slug)
	_, modelPart, ok := strings.Cut(trimmed, "/")
	if !ok || strings.TrimSpace(modelPart) == "" {
		return trimmed
	}
	return strings.TrimSpace(modelPart)
}

func isOpenCodeModelFree(providerID, modelID string, hasCost bool, inputCost, outputCost float64) bool {
	provLower := strings.ToLower(strings.TrimSpace(providerID))
	idLower := strings.ToLower(strings.TrimSpace(modelID))
	isBuiltInOC := provLower == "opencode" || provLower == "opencode-go"
	hasFreeSuffix := strings.HasSuffix(idLower, "-free") || strings.HasSuffix(idLower, ":free")
	if hasCost {
		if inputCost > 0 || outputCost > 0 {
			return false
		}
		return isBuiltInOC || hasFreeSuffix
	}
	return hasFreeSuffix || (provLower == "opencode" && idLower == "big-pickle")
}

func openCodeDefaultEffort(efforts []string) string {
	for _, pref := range []string{"medium", "high"} {
		for _, e := range efforts {
			if e == pref {
				return e
			}
		}
	}
	if len(efforts) > 0 {
		return efforts[0]
	}
	return ""
}

func skipSpaces(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// ParseOpenCodeModelsOutput parses both `opencode models --verbose` (slug line
// followed by a JSON metadata object) and plain `opencode models` (one
// `<provider>/<model>` slug per line). Free built-in OpenCode models are
// ranked first so fresh installs surface ready-to-use 0x models immediately.
func ParseOpenCodeModelsOutput(raw string) []ModelItem {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	seen := make(map[string]bool)
	var items []ModelItem

	for i := skipSpaces(normalized, 0); i < len(normalized); i = skipSpaces(normalized, i) {
		if normalized[i] == '{' {
			dec := json.NewDecoder(strings.NewReader(normalized[i:]))
			var skip json.RawMessage
			if err := dec.Decode(&skip); err == nil && dec.InputOffset() > 0 {
				i += int(dec.InputOffset())
				continue
			}
			if nl := strings.IndexByte(normalized[i:], '\n'); nl >= 0 {
				i += nl + 1
			} else {
				break
			}
			continue
		}

		nl := strings.IndexByte(normalized[i:], '\n')
		var line string
		if nl >= 0 {
			line = normalized[i : i+nl]
			i += nl + 1
		} else {
			line = normalized[i:]
			i = len(normalized)
		}
		slug := strings.TrimSpace(line)
		if slug == "" || strings.ContainsAny(slug, " \t") || !strings.Contains(slug, "/") {
			continue
		}

		var doc *rawOpenCodeVerboseModel
		next := skipSpaces(normalized, i)
		if next < len(normalized) && normalized[next] == '{' {
			dec := json.NewDecoder(strings.NewReader(normalized[next:]))
			var parsed rawOpenCodeVerboseModel
			if err := dec.Decode(&parsed); err == nil && dec.InputOffset() > 0 {
				doc = &parsed
				i = next + int(dec.InputOffset())
			}
		}

		if seen[slug] {
			continue
		}
		seen[slug] = true

		provPrefix, modelPart, _ := strings.Cut(slug, "/")
		if doc != nil {
			if strings.EqualFold(strings.TrimSpace(doc.Status), "deprecated") {
				continue
			}
			provID := strings.TrimSpace(doc.ProviderID)
			if provID == "" {
				provID = strings.TrimSpace(provPrefix)
			}
			modelID := strings.TrimSpace(doc.ID)
			if modelID == "" {
				modelID = strings.TrimSpace(modelPart)
			}
			hasCost := doc.Cost != nil
			var inCost, outCost float64
			if doc.Cost != nil {
				inCost = doc.Cost.Input
				outCost = doc.Cost.Output
			}
			isFree := isOpenCodeModelFree(provID, modelID, hasCost, inCost, outCost)
			mult := ""
			if isFree {
				mult = "0x"
				inCost = 0
				outCost = 0
			}
			name := strings.TrimSpace(doc.Name)
			if name == "" {
				name = formatOpenCodeModelName(slug)
			}
			ctxLimit := 200000
			outLimit := 64000
			if doc.Limit != nil {
				if doc.Limit.Context > 0 {
					ctxLimit = doc.Limit.Context
				}
				if doc.Limit.Output > 0 {
					outLimit = doc.Limit.Output
				}
			}
			reasoning := false
			image := false
			if doc.Capabilities != nil {
				reasoning = doc.Capabilities.Reasoning
				image = doc.Capabilities.Attachment || (doc.Capabilities.Input != nil && doc.Capabilities.Input.Image)
			}
			var rawEfforts []string
			for k, v := range doc.Variants {
				if !v.Disabled {
					rawEfforts = append(rawEfforts, k)
				}
			}
			efforts := NormalizeEffortList(rawEfforts)
			if len(efforts) > 0 {
				reasoning = true
			}
			items = append(items, ModelItem{
				ID:            slug,
				Name:          name,
				ProviderID:    RuntimeOpenCode,
				Runtime:       RuntimeOpenCode,
				Reasoning:     reasoning,
				Efforts:       efforts,
				DefaultEffort: openCodeDefaultEffort(efforts),
				Image:         image,
				Free:          isFree,
				Multiplier:    mult,
				ContextLimit:  ctxLimit,
				OutputLimit:   outLimit,
				InputCost:     inCost,
				OutputCost:    outCost,
			})
			continue
		}

		isFree := isOpenCodeModelFree(provPrefix, modelPart, false, 0, 0)
		mult := ""
		if isFree {
			mult = "0x"
		}
		items = append(items, ModelItem{
			ID:           slug,
			Name:         formatOpenCodeModelName(slug),
			ProviderID:   RuntimeOpenCode,
			Runtime:      RuntimeOpenCode,
			Reasoning:    true,
			Efforts:      []string{"low", "medium", "high"},
			Free:         isFree,
			Multiplier:   mult,
			ContextLimit: 200000,
			OutputLimit:  64000,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		rank := func(m ModelItem) int {
			if m.Free && strings.HasPrefix(strings.ToLower(m.ID), "opencode/") {
				return 0
			}
			if m.Free {
				return 1
			}
			return 2
		}
		return rank(items[i]) < rank(items[j])
	})
	if len(items) > 120 {
		items = items[:120]
	}
	return items
}

func runOpenCodeModelsCmd(bin string, args ...string) []ModelItem {
	ctx, cancel := context.WithTimeout(context.Background(), ocCLIProbeTimeout)
	defer cancel()
	cmd := subproc.CommandContext(ctx, bin, args...)
	cmd.Env = WithSystemProxy(WithUserBinPath(os.Environ()))
	cmd.WaitDelay = time.Second
	raw, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return ParseOpenCodeModelsOutput(string(raw))
}

// DiscoverOpenCodeCLIModelsWithRefresh queries `opencode models --verbose`
// (falling back to plain `opencode models`, cached 2m) for single-turn CLI mode.
func DiscoverOpenCodeCLIModelsWithRefresh(forceRefresh bool) []ModelItem {
	bin := ResolveOpenCodeBinary()
	if bin == "" {
		return nil
	}
	ocCLIModelsMu.Lock()
	defer ocCLIModelsMu.Unlock()
	if !forceRefresh && ocCLICachedBin == bin && len(ocCLICachedList) > 0 && time.Since(ocCLICachedTime) < 2*time.Minute {
		return append([]ModelItem(nil), ocCLICachedList...)
	}

	items := runOpenCodeModelsCmd(bin, "models", "--verbose")
	if len(items) == 0 {
		items = runOpenCodeModelsCmd(bin, "models")
	}
	if len(items) > 0 {
		ocCLICachedBin = bin
		ocCLICachedTime = time.Now()
		ocCLICachedList = items
		return append([]ModelItem(nil), items...)
	}
	if ocCLICachedBin == bin {
		return append([]ModelItem(nil), ocCLICachedList...)
	}
	return nil
}

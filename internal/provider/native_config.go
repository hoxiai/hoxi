package provider

import (
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxiconfig"
)

// LoadNativeConfigDoc loads legacy local settings for the execution directory,
// never the gateway's primary project when another workspace is active. Shared
// hoxi.json is overlaid separately by the native engine and is never persisted.
func (s *Service) LoadNativeConfigDoc(directory string) map[string]any {
	doc := map[string]any{}
	paths := []string{userHoxiModelsPath()}
	if directory = strings.TrimSpace(directory); directory != "" {
		paths = append(paths, filepath.Join(directory, ".hoxi", "models.json"))
	}
	for _, path := range paths {
		if layer := readJSONMapFile(path); layer != nil {
			deepMergeMap(doc, layer)
		}
	}
	ensureCompactionDefaults(doc)
	return doc
}

// ConfiguredCompactionModel selects the destination for a background request.
// A repository's settings must not redirect the transcript to another provider.
func (s *Service) ConfiguredCompactionModel() string {
	cfg := s.LoadNativeConfigDoc("")
	agents, _ := cfg["agent"].(map[string]any)
	compaction, _ := agents["compaction"].(map[string]any)
	model, _ := compaction["model"].(string)
	if strings.TrimSpace(model) == "" {
		model, _ = cfg["small_model"].(string)
	}
	return strings.TrimSpace(model)
}

// LoadProjectConfigDoc is the effective, read-only project view. Consumers must
// not feed this document to PersistHoxiConfigDoc or UpdateHoxiConfigDoc.
func (s *Service) LoadProjectConfigDoc(directory string) (map[string]any, error) {
	project, err := hoxiconfig.Load(directory)
	if err != nil {
		return nil, err
	}
	return hoxiconfig.Overlay(s.LoadNativeConfigDoc(directory), project), nil
}

func sanitizeJSONCBytes(raw []byte) ([]byte, error) {
	return hoxiconfig.SanitizeJSONC(raw)
}

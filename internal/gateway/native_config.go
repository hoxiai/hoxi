package gateway

import (
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func (g *Gateway) nativeConfigDirectory(r *http.Request) string {
	if r != nil {
		if dir := strings.TrimSpace(r.URL.Query().Get("directory")); dir != "" {
			return dir
		}
	}
	return g.rootDir
}

func (g *Gateway) nativeProjectConfig(dir string) (map[string]any, error) {
	if g.providers == nil {
		return nil, nil
	}
	return g.providers.LoadProjectConfigDoc(dir)
}

func (g *Gateway) handleNativeAgentList(w http.ResponseWriter, r *http.Request) {
	dir := g.nativeConfigDirectory(r)
	doc, err := g.nativeProjectConfig(dir)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	active, _ := llm.LoadAgents(dir, doc)
	writeJSON(w, http.StatusOK, active)
}

func (g *Gateway) handleNativeCommandList(w http.ResponseWriter, r *http.Request) {
	dir := g.nativeConfigDirectory(r)
	doc, err := g.nativeProjectConfig(dir)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, llm.LoadCommands(dir, doc))
}

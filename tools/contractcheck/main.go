// Command contractcheck enforces Hoxi's host-repository architectural redlines:
//  1. Unidirectional 4-tier Go package dependency DAG (app/cli -> gateway -> domain -> base) with 0 cycles.
//  2. 800-line ceiling and snake_case hygiene across all host Go files.
//  3. Explicit Transport-backed http.Client construction in internal/provider and zero bare net/http helpers.
//  4. Byte-for-byte esbuild & policy-token synchronization of internal/opencode/browser_plugin.mjs.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultModulePath = "github.com/hoxiai/hoxi"
	maxGoFileLines    = 800
)

var ignoredDirs = map[string]bool{
	".git":           true,
	".hoxi":          true,
	".claude":        true,
	".opencode":      true,
	".agents":        true,
	".ai":            true,
	".vscode":        true,
	".idea":          true,
	"work":           true,
	"client":         true,
	"mobile":         true,
	"site":           true,
	".dart_tool":     true,
	".pub-cache":     true,
	"node_modules":   true,
	".pnpm-store":    true,
	"vendor":         true,
	"dist":           true,
	"build":          true,
	"wailsjs":        true,
	"Pods":           true,
	"DerivedData":    true,
	"ephemeral":      true,
	"go-build":       true,
	"go-build-cache": true,
	".gocache":       true,
	".gomodcache":    true,
}

// Tier 0 base packages: pure foundational utilities and leaf types.
// Base packages may ONLY import other Tier 0 base packages.
var goBasePackages = map[string]bool{
	"agentcli":            true,
	"agyhook":             true,
	"buildinfo":           true,
	"canonical":           true,
	"claudeconfig":        true,
	"dataprofiles":        true,
	"diagnostics":         true,
	"hoxiconfig":          true,
	"hoxipaths":           true,
	"llm":                 true,
	"notify":              true,
	"opencode/ocapi":      true,
	"opencode/ocstate":    true,
	"osopen":              true,
	"reclaim":             true,
	"runner/sessionstore": true,
	"runner/taskledger":   true,
	"runner/turnstream":   true,
	"sqlitecli":           true,
	"statusbar":           true,
	"subproc":             true,
	"testutil/execstub":   true,
	"textindex":           true,
}

// Explicit cross-domain edges permitted between Tier 1 domain packages.
// All other cross-domain coordination belongs in Tier 2 (internal/gateway).
var allowedDomainImports = map[string]map[string]bool{
	"opencode": {"sandbox": true},
	"provider": {"skills": true},
	"runner": {
		"attachment":         true,
		"runner/memstore":    true,
		"runner/procgroup":   true,
		"runner/shellpolicy": true,
		"sandbox":            true,
		"worktree":           true,
	},
	"runner/memstore":    {"worktree": true},
	"runner/procgroup":   {"runner/shellpolicy": true},
	"runner/shellpolicy": {"sandbox": true, "worktree": true},
	"telemetry":          {"auth": true},
}

// Files in internal/provider permitted to construct &http.Client{Transport: ...}.
// Media generation and auxiliary files must route through callSimpleLLM,
// newSafeVideoHTTPClient, or selectImageTransport rather than building raw clients.
var providerTransportAllowlist = map[string]bool{
	"audio_asr.go":           true,
	"audio_tts.go":           true,
	"http.go":                true,
	"mcp_oauth_provision.go": true,
	"oauth_refresh.go":       true,
	"provider.go":            true,
	"relay.go":               true,
	"relay_extensions.go":    true,
	"stream_client.go":       true,
	"video_gen.go":           true,
}

// Violation represents a single deterministic contract failure.
type Violation struct {
	Rule    string
	File    string
	Line    int
	Col     int
	Message string
}

func (v Violation) String() string {
	if v.Line > 0 && v.Col > 0 {
		return fmt.Sprintf("[%s] %s:%d:%d: %s", v.Rule, v.File, v.Line, v.Col, v.Message)
	}
	if v.Line > 0 {
		return fmt.Sprintf("[%s] %s:%d: %s", v.Rule, v.File, v.Line, v.Message)
	}
	return fmt.Sprintf("[%s] %s: %s", v.Rule, v.File, v.Message)
}

// Stats summarizes checked repository artifacts.
type Stats struct {
	GoFiles          int
	InternalPackages int
	PluginModules    int
	PluginBundleMode string
}

type edgeRef struct {
	file string
	line int
	col  int
}

func main() {
	rootFlag := flag.String("root", ".", "path to host repository root")
	flag.Parse()

	absRoot, err := filepath.Abs(*rootFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[contractcheck] failed to resolve root: %v\n", err)
		os.Exit(2)
	}

	violations, stats, err := Run(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[contractcheck] execution error: %v\n", err)
		os.Exit(2)
	}
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "  ✗ %s\n", v.String())
		}
		fmt.Fprintf(os.Stderr, "\n[contractcheck] ✗ %d contract violation(s) found.\n", len(violations))
		os.Exit(1)
	}
	fmt.Printf(
		"[contractcheck] ✓ %d Go files, %d internal packages (strict DAG), provider HTTP transports, and browser_plugin.mjs (%s, %d modules) verified.\n",
		stats.GoFiles,
		stats.InternalPackages,
		stats.PluginBundleMode,
		stats.PluginModules,
	)
}

// Run executes all repository contract checks against repoRoot.
func Run(repoRoot string) ([]Violation, Stats, error) {
	var violations []Violation
	var stats Stats

	modulePath := readModulePath(filepath.Join(repoRoot, "go.mod"))
	internalPrefix := modulePath + "/internal/"

	var goFiles []string
	err := filepath.WalkDir(repoRoot, func(fullPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fullPath == repoRoot {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if ignoredDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") {
			goFiles = append(goFiles, fullPath)
		}
		return nil
	})
	if err != nil {
		return nil, stats, err
	}
	sort.Strings(goFiles)
	stats.GoFiles = len(goFiles)

	fset := token.NewFileSet()
	internalGraph := make(map[string]map[string]bool)
	edgeLocations := make(map[string]edgeRef)

	for _, fullPath := range goFiles {
		relNative, relErr := filepath.Rel(repoRoot, fullPath)
		if relErr != nil {
			return nil, stats, relErr
		}
		rel := filepath.ToSlash(relNative)
		baseName := path.Base(rel)

		raw, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			return nil, stats, readErr
		}

		if lines := countLines(raw); lines > maxGoFileLines {
			violations = append(violations, Violation{
				Rule:    "line-limit",
				File:    rel,
				Line:    maxGoFileLines + 1,
				Message: fmt.Sprintf("%d lines exceeds %d-line hard ceiling; split by single responsibility", lines, maxGoFileLines),
			})
		}

		if strings.HasPrefix(baseName, "zz_scratch_") {
			violations = append(violations, Violation{
				Rule:    "scratch-file",
				File:    rel,
				Message: "temporary zz_scratch_* file must be removed or promoted to a named *_test.go file",
			})
		}

		if (strings.HasPrefix(rel, "internal/") || strings.HasPrefix(rel, "cmd/") || strings.HasPrefix(rel, "tools/")) && !isSnakeCaseGoFile(baseName) {
			violations = append(violations, Violation{
				Rule:    "file-naming",
				File:    rel,
				Message: "Go filename must use lowercase snake_case.go",
			})
		}

		astFile, parseErr := parser.ParseFile(fset, fullPath, raw, 0)
		if parseErr != nil {
			pos := fset.Position(astFilePos(astFile))
			violations = append(violations, Violation{
				Rule:    "go-syntax",
				File:    rel,
				Line:    pos.Line,
				Col:     pos.Column,
				Message: parseErr.Error(),
			})
			continue
		}

		if strings.HasSuffix(baseName, "_test.go") {
			continue
		}

		if strings.HasPrefix(rel, "tools/") {
			for _, imp := range astFile.Imports {
				impPath, unqErr := strconv.Unquote(imp.Path.Value)
				if unqErr != nil || !strings.HasPrefix(impPath, internalPrefix) {
					continue
				}
				target := strings.TrimPrefix(impPath, internalPrefix)
				if target == "app" || target == "cli" || target == "gateway" {
					p := fset.Position(imp.Pos())
					violations = append(violations, Violation{
						Rule:    "layer-dag",
						File:    rel,
						Line:    p.Line,
						Col:     p.Column,
						Message: fmt.Sprintf("tools package %s must not import host top-tier internal/%s", path.Dir(rel), target),
					})
				}
			}
			continue
		}

		if !strings.HasPrefix(rel, "internal/") {
			continue
		}

		pkg := strings.TrimPrefix(path.Dir(rel), "internal/")
		if internalGraph[pkg] == nil {
			internalGraph[pkg] = make(map[string]bool)
		}

		for _, imp := range astFile.Imports {
			impPath, unqErr := strconv.Unquote(imp.Path.Value)
			if unqErr != nil || !strings.HasPrefix(impPath, internalPrefix) {
				continue
			}
			target := strings.TrimPrefix(impPath, internalPrefix)
			pos := fset.Position(imp.Pos())
			if target != pkg {
				internalGraph[pkg][target] = true
				key := pkg + "->" + target
				if _, exists := edgeLocations[key]; !exists {
					edgeLocations[key] = edgeRef{file: rel, line: pos.Line, col: pos.Column}
				}
			}
			if v, bad := checkInternalImportEdge(rel, pos, pkg, target); bad {
				violations = append(violations, v)
			}
		}

		violations = append(violations, checkInternalASTContracts(fset, astFile, rel, baseName, pkg)...)
	}

	stats.InternalPackages = len(internalGraph)
	violations = append(violations, detectImportCycles(internalGraph, edgeLocations)...)

	pluginViolations, pluginModules, bundleMode := checkBrowserPluginSync(repoRoot)
	stats.PluginModules = pluginModules
	stats.PluginBundleMode = bundleMode
	violations = append(violations, pluginViolations...)

	sortViolations(violations)
	return violations, stats, nil
}

func checkInternalImportEdge(rel string, pos token.Position, pkg, target string) (Violation, bool) {
	if target == pkg {
		return Violation{}, false
	}
	mk := func(msg string) (Violation, bool) {
		return Violation{
			Rule:    "layer-dag",
			File:    rel,
			Line:    pos.Line,
			Col:     pos.Column,
			Message: msg,
		}, true
	}

	switch {
	case pkg == "app":
		return Violation{}, false
	case pkg == "cli":
		if target == "app" {
			return mk("internal/cli must not reverse-import internal/app")
		}
		return Violation{}, false
	case pkg == "gateway":
		if target == "app" || target == "cli" {
			return mk(fmt.Sprintf("internal/gateway must not reverse-import internal/%s", target))
		}
		return Violation{}, false
	case goBasePackages[pkg]:
		if !goBasePackages[target] {
			return mk(fmt.Sprintf("base package internal/%s must only import Tier 0 base packages (found internal/%s)", pkg, target))
		}
		return Violation{}, false
	default:
		if target == "app" || target == "cli" || target == "gateway" {
			return mk(fmt.Sprintf("domain package internal/%s must not reverse-import top-tier internal/%s", pkg, target))
		}
		if target == "provider" {
			return mk(fmt.Sprintf("domain package internal/%s must not import internal/provider directly; depend on internal/llm interfaces injected by internal/gateway", pkg))
		}
		if !goBasePackages[target] && !allowedDomainImports[pkg][target] {
			return mk(fmt.Sprintf("domain package internal/%s must not cross-import domain package internal/%s; wire cross-domain coordination in internal/gateway", pkg, target))
		}
		return Violation{}, false
	}
}

func detectImportCycles(graph map[string]map[string]bool, edgeLocations map[string]edgeRef) []Violation {
	pkgs := make([]string, 0, len(graph))
	for pkg := range graph {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	state := make(map[string]int, len(pkgs)) // 0=unvisited, 1=visiting, 2=visited
	var stack []string
	stackIndex := make(map[string]int)
	seenCycles := make(map[string]bool)
	var violations []Violation

	var dfs func(u string)
	dfs = func(u string) {
		state[u] = 1
		stackIndex[u] = len(stack)
		stack = append(stack, u)

		neighbors := make([]string, 0, len(graph[u]))
		for v := range graph[u] {
			neighbors = append(neighbors, v)
		}
		sort.Strings(neighbors)

		for _, v := range neighbors {
			switch state[v] {
			case 0:
				dfs(v)
			case 1:
				start := stackIndex[v]
				cycle := canonicalCycleSlice(stack[start:])
				canon := strings.Join(cycle, "->")
				if !seenCycles[canon] {
					seenCycles[canon] = true
					chain := make([]string, 0, len(cycle)+1)
					for _, node := range cycle {
						chain = append(chain, "internal/"+node)
					}
					chain = append(chain, "internal/"+cycle[0])
					loc := edgeLocations[u+"->"+v]
					violations = append(violations, Violation{
						Rule:    "layer-cycle",
						File:    loc.file,
						Line:    loc.line,
						Col:     loc.col,
						Message: fmt.Sprintf("import cycle forbidden in internal DAG: %s", strings.Join(chain, " -> ")),
					})
				}
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackIndex, u)
		state[u] = 2
	}

	for _, pkg := range pkgs {
		if state[pkg] == 0 {
			dfs(pkg)
		}
	}
	return violations
}

func canonicalCycleSlice(cycle []string) []string {
	if len(cycle) == 0 {
		return nil
	}
	minIdx := 0
	for i := 1; i < len(cycle); i++ {
		if cycle[i] < cycle[minIdx] {
			minIdx = i
		}
	}
	rotated := make([]string, 0, len(cycle))
	rotated = append(rotated, cycle[minIdx:]...)
	rotated = append(rotated, cycle[:minIdx]...)
	return rotated
}

func readModulePath(goModPath string) string {
	raw, err := os.ReadFile(goModPath)
	if err != nil {
		return defaultModulePath
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			if mod := strings.TrimSpace(strings.TrimPrefix(line, "module ")); mod != "" {
				return mod
			}
		}
	}
	return defaultModulePath
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func isSnakeCaseGoFile(name string) bool {
	if !strings.HasSuffix(name, ".go") || len(name) <= 3 {
		return false
	}
	stem := strings.TrimSuffix(name, ".go")
	for i := 0; i < len(stem); i++ {
		c := stem[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func astFilePos(f *ast.File) token.Pos {
	if f == nil {
		return token.NoPos
	}
	return f.Pos()
}

func sortViolations(vs []Violation) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].File != vs[j].File {
			return vs[i].File < vs[j].File
		}
		if vs[i].Line != vs[j].Line {
			return vs[i].Line < vs[j].Line
		}
		if vs[i].Col != vs[j].Col {
			return vs[i].Col < vs[j].Col
		}
		return vs[i].Rule < vs[j].Rule
	})
}

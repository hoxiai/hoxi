package codeindex

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"regexp"
	"strings"
	"sync"
)

// Resolve ambiguous Go selectors from the same indexed snapshot as the graph.
// Type checking is local to a package and never loads dependencies or runs Go tools.
type goCallCache struct {
	packages sync.Map // directory -> func() goCallSites
}

type goCallSite struct {
	name, receiver, targetPath string
	targetLine                 int
	invoked                    bool
}

type goCallSites map[string]map[int][]goCallSite

func (c *cachedIndex) goSites(file string) map[int][]goCallSite {
	dir := path.Dir(file)
	lazy, ok := c.goCalls.packages.Load(dir)
	if !ok {
		lazy, _ = c.goCalls.packages.LoadOrStore(dir, sync.OnceValue(func() goCallSites { return c.buildGoCallSites(dir) }))
	}
	return lazy.(func() goCallSites)()[file]
}

func (c *cachedIndex) buildGoCallSites(dir string) goCallSites {
	fset := token.NewFileSet()
	groups := map[string][]*ast.File{}
	out := goCallSites{}
	for _, p := range c.sortedPaths {
		if path.Dir(p) != dir || path.Ext(p) != ".go" || isProtectedOrSecretRelPath(p) {
			continue
		}
		var lines []string
		for _, ch := range c.index.Files[p].Chunks {
			if ch.StartLine < 1 || ch.EndLine < ch.StartLine || int64(ch.EndLine) > c.index.Files[p].Size+1 {
				continue
			}
			for len(lines) < ch.EndLine {
				lines = append(lines, "")
			}
			copy(lines[ch.StartLine-1:ch.EndLine], strings.Split(ch.Text, "\n"))
		}
		f, _ := parser.ParseFile(fset, p, strings.Join(lines, "\n"), parser.SkipObjectResolution)
		if f != nil && f.Name != nil {
			groups[f.Name.Name] = append(groups[f.Name.Name], f)
		}
	}
	for name, files := range groups {
		info := types.Info{Uses: map[*ast.Ident]types.Object{}}
		cfg := types.Config{Error: func(error) {}}
		_, _ = cfg.Check(dir+"/"+name, fset, files, &info)
		for _, f := range files {
			p := fset.PositionFor(f.Pos(), false).Filename
			byLine := map[int][]goCallSite{}
			invocations := map[*ast.Ident]bool{}
			receivers := map[*ast.Ident]string{}
			ast.Inspect(f, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					switch fun := ast.Unparen(node.Fun).(type) {
					case *ast.Ident:
						invocations[fun] = true
					case *ast.SelectorExpr:
						invocations[fun.Sel] = true
					}
				case *ast.SelectorExpr:
					receiver := ""
					switch expr := ast.Unparen(node.X).(type) {
					case *ast.Ident:
						receiver = expr.Name
					case *ast.SelectorExpr:
						receiver = expr.Sel.Name
					}
					receivers[node.Sel] = receiver
				case *ast.Ident:
					if !ambiguousGoMethod(c.symbols[strings.ToLower(node.Name)], node.Name) {
						return true
					}
					obj := info.Uses[node]
					target, isFunc := obj.(*types.Func)
					receiver, isSelector := receivers[node]
					if !isFunc && (obj != nil || (!isSelector && !invocations[node])) {
						return true
					}
					site := goCallSite{name: node.Name, receiver: receiver, invoked: invocations[node]}
					if isFunc {
						recv := target.Type().(*types.Signature).Recv()
						if recv == nil || !types.IsInterface(recv.Type()) {
							def := fset.PositionFor(target.Origin().Pos(), false)
							site.targetPath, site.targetLine = def.Filename, def.Line
						}
					}
					line := fset.PositionFor(node.Pos(), false).Line
					byLine[line] = append(byLine[line], site)
				}
				return true
			})
			out[p] = byLine
		}
	}
	return out
}

func ambiguousGoMethod(cands []symbolDecl, name string) bool {
	count := 0
	method := false
	for _, cand := range cands {
		if path.Ext(cand.Path) == ".go" && cand.Symbol == name {
			count++
			method = method || cand.Receiver != ""
		}
	}
	return count > 1 && method
}

func (c *cachedIndex) goTargets(file string, line int, name, receiver string, cands []symbolDecl, invocationsOnly bool) (targets []symbolDecl, possible bool) {
	sites := c.goSites(file)[line]
	seen := map[symbolDecl]bool{}
	for _, site := range sites {
		if site.name != name || (invocationsOnly && !site.invoked) || (receiver != "" && site.receiver != receiver) {
			continue
		}
		for _, d := range cands {
			if d.Symbol != name || path.Ext(d.Path) != ".go" {
				continue
			}
			if site.targetPath != "" && (site.targetPath != d.Path || site.targetLine != d.StartLine) {
				continue
			}
			if site.targetPath == "" {
				possible = true
			}
			if !seen[d] {
				targets = append(targets, d)
				seen[d] = true
			}
		}
	}
	return
}

func (c *cachedIndex) goCallerMatch(file string, line int, name, symbol, targetPath string, cands []symbolDecl) (match, possible bool) {
	targets, possible := c.goTargets(file, line, name, "", cands, false)
	qualified := strings.TrimSuffix(symbol, "."+name)
	for _, d := range targets {
		if targetPath != "" && d.Path != targetPath {
			continue
		}
		if qualified != symbol && qualified != d.Receiver && qualified != path.Base(path.Dir(d.Path)) {
			continue
		}
		return true, possible
	}
	return false, false
}

// Keep ambiguity visible in the native tool and in clients that show snippets.
func calleeCandidates(c *cachedIndex, file string, line int, name, receiver string, cands []symbolDecl, imports []string) ([]symbolDecl, bool) {
	if path.Ext(file) == ".go" && ambiguousGoMethod(cands, name) {
		return c.goTargets(file, line, name, receiver, cands, true)
	}
	if d, ok := pickBestSymbolDecl(cands, file, name, receiver, imports); ok {
		return []symbolDecl{d}, false
	}
	return nil, false
}

var goMethodReceiverRE = regexp.MustCompile(`^\s*func\s*\(\s*(?:[[:word:]]+\s+)?\*?([[:word:]]+)(?:\[[^]]+\])?\s*\)`)

func goReceiverName(line string) string {
	m := goMethodReceiverRE.FindStringSubmatch(line)
	if len(m) == 0 {
		return ""
	}
	return m[1]
}

package codeindex

import (
	"path/filepath"
	"testing"
)

func TestGraphDisambiguatesGoReceiversAndMarksInterfaceCalls(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha.go"), "package sample\n\ntype Alpha struct{}\nfunc (*Alpha) Flush() {}\n")
	writeFile(t, filepath.Join(dir, "beta.go"), "package sample\ntype Beta struct{}\nfunc (Beta) Flush() {}\n")
	writeFile(t, filepath.Join(dir, "use.go"), "package sample\nfunc DrainAlpha(a *Alpha) {\n a.Flush()\n}\nfunc DrainBeta(b Beta) {\n b.Flush()\n}\nfunc DrainAny(x interface{ Flush() }) {\n x.Flush()\n}\n")
	svc := NewService(t.TempDir())
	for _, tc := range []struct{ caller, file string }{{"DrainAlpha", "alpha.go"}, {"DrainBeta", "beta.go"}} {
		callees, err := svc.Callees(dir, tc.caller, "use.go", 10)
		if err != nil || len(callees.Callees) != 1 || callees.Callees[0].Path != tc.file || callees.Callees[0].Possible {
			t.Fatalf("%s calls %+v: %v", tc.caller, callees, err)
		}
		callers, err := svc.Callers(dir, "Flush", tc.file, 10)
		if err != nil {
			t.Fatal(err)
		}
		matches := map[string]bool{}
		for _, hit := range callers.Callers {
			matches[hit.Symbol] = hit.Possible
		}
		if possible, ok := matches[tc.caller]; len(matches) != 2 || !ok || possible || !matches["DrainAny"] {
			t.Fatalf("%s callers: %+v", tc.file, callers.Callers)
		}
	}
	candidates, err := svc.Callees(dir, "DrainAny", "use.go", 10)
	if err != nil || len(candidates.Callees) != 2 {
		t.Fatalf("interface candidates: %+v, %v", candidates, err)
	}
	for _, hit := range candidates.Callees {
		if !hit.Possible {
			t.Fatalf("interface call marked definitive: %+v", hit)
		}
	}
}

func TestGoGraphQualifiedMethodsAndMethodValuesInOneFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package demo
type Alpha struct{}
type Beta struct{}
type Pair struct{ A Alpha; B Beta }
func (Alpha) Flush() {}
func (Beta) Flush() {}
func UseA(pair Pair) {
    pair.A.Flush()
}
func CaptureB(pair Pair) func() {
    return pair.B.Flush
}
`)
	svc := NewService(t.TempDir())
	for _, tc := range []struct {
		receiver, caller string
		line             int
	}{{"Alpha", "UseA", 5}, {"Beta", "CaptureB", 6}} {
		symbol := tc.receiver + ".Flush"
		got, err := svc.Callers(dir, symbol, "main.go", 10)
		if err != nil || len(got.Callers) != 1 || got.Callers[0].Symbol != tc.caller || got.Callers[0].Possible {
			t.Fatalf("%s callers: %+v %v", symbol, got, err)
		}
		res, err := svc.Context(dir, "", symbol, "", 1, 10)
		if err != nil || len(res.Callers) != 1 || res.Callers[0].Symbol != tc.caller {
			t.Fatalf("%s context: %+v %v", symbol, res, err)
		}
		out, err := svc.Callees(dir, tc.caller, "main.go", 10)
		if err != nil || tc.caller == "CaptureB" && len(out.Callees) != 0 || tc.caller == "UseA" && (len(out.Callees) != 1 || out.Callees[0].StartLine != tc.line) {
			t.Fatalf("%s callees: %+v %v", tc.caller, out, err)
		}
	}
}

package codeindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectTypeScriptMethodAcrossLinesAndClasses(t *testing.T) {
	for _, ext := range []string{".ts", ".tsx"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			cache := t.TempDir()
			path := "billing" + ext
			source := "class Cash { charge(){return 0} }\nexport class Billing<T> {\n  // fake() {\n  async charge<U>(\n    cents: number,\n  ): Promise<number> {\n    const literal = /[}]/;\n    return cents;\n  }\n}\n"
			if err := os.WriteFile(filepath.Join(dir, path), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			m := typeScriptMethods(path, source)
			if len(m) != 2 || m[1].Receiver != "Billing" || m[1].StartLine != 4 || m[1].EndLine != 9 {
				t.Fatalf("methods %+v", m)
			}
			for _, file := range []string{path, ""} {
				s := NewService(cache)
				got, err := s.Inspect(dir, file, "Billing.charge", 0, 0, 1)
				if err != nil || got.Symbol != "Billing.charge" || !strings.Contains(got.Content, "return cents") || strings.Contains(got.Content, "return 0") {
					t.Fatalf("inspect %+v %v", got, err)
				}
				if _, err := s.Inspect(dir, file, "Missing.charge", 0, 0, 1); err == nil {
					t.Fatal("matched the wrong class")
				}
			}
		})
	}
}

func TestTypeScriptObjectReturnOverloadsResolveToImplementation(t *testing.T) {
	source := "class Ledger {\n  $get(n: number): {value: number};\n  $get<T extends {v: string; w: number}>(n: T): {value: T} | {missing: true} {\n    return {value: n};\n  }\n}"
	methods := typeScriptMethods("ledger.ts", source)
	if len(methods) != 1 || methods[0].Symbol != "$get" || methods[0].StartLine != 3 || methods[0].EndLine != 5 {
		t.Fatalf("wrong method span: %+v", methods)
	}
}

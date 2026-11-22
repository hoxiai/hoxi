package shellpolicy

import (
	"strings"
	"testing"
)

func TestInlineDiscoveryDirectoryOnlySuggestsAScopedRead(t *testing.T) {
	const code = `const fs = require("fs"); const f = fs.readdirSync("dist").find(x => x.startsWith("bundle-")); console.log(fs.readFileSync("dist/" + f, "utf8"));`
	node := func(code string) string { return "node -e '" + code + "'" }
	tests := []struct{ name, command, dir string }{
		{"concat", node(code), "dist"},
		{"relative listing", node(strings.Replace(code, `"dist"`, `"./dist"`, 1)), "dist"},
		{"relative read", node(strings.Replace(code, `"dist/"`, `"./dist/"`, 1)), "dist"},
		{"join", node(strings.Replace(code, `"dist/" + f`, `path.join("dist", f)`, 1)), "dist"},
		{"different directory", node(strings.Replace(code, `"dist/"`, `"other/"`, 1)), ""},
		{"different variable", node(strings.Replace(code, "+ f,", "+ g,", 1)), ""},
		{"outside directory", node(strings.ReplaceAll(code, "dist", "../outside")), ""},
		{"protected directory", node(strings.ReplaceAll(code, "dist", ".git")), ""},
		{"credential literal", node(strings.Replace(code, "bundle-", ".env", 1)), ""},
		{"second read", node(code + `fs.readFileSync(process.argv[1]);`), ""},
		{"write", node(code + `fs.writeFileSync("out.txt", "text");`), ""},
		{"chained command", node(code) + " && git status", ""},
		{"explicit approval", "# hoxi:unsandboxed\n" + node(code), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := InlineDiscoveryDirectory(tc.command, nil); got != tc.dir {
				t.Fatalf("suggested directory %q, want %q", got, tc.dir)
			}
			if tc.dir != "" && !isOpaqueShellCommandWith(tc.command, "/workspace", scriptTrustReadOnly) {
				t.Fatal("a suggestion must never approve the original command")
			}
		})
	}
}

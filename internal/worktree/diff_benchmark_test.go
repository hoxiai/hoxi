package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Compare the former eager patch path with the bounded bulk-list path on the
// same repository. Both include Git's numstat work; neither uses a warmed diff cache.
func BenchmarkGeneratedDiffPatches(b *testing.B) {
	dir := b.TempDir()
	git := func(args ...string) string {
		b.Helper()
		out, err := runGitRaw(dir, args...)
		if err != nil {
			b.Fatal(err)
		}
		return out
	}
	git("init", "-b", "main")
	git("config", "user.name", "Test User")
	git("config", "user.email", "test@hoxi.local")
	write := func(version string) {
		b.Helper()
		var content strings.Builder
		for i := 0; i < 12000; i++ {
			fmt.Fprintf(&content, "const entry_%d = %q; // %s\n", i, version, strings.Repeat("x", 100))
		}
		for _, name := range []string{"a.generated.ts", "b.generated.ts"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content.String()), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	write("before")
	git("add", "-A")
	git("commit", "-m", "benchmark fixture")
	write("after")
	porcelain := git("--no-optional-locks", "status", "--porcelain", "-b", "-z")
	b.Run("eager-patch-baseline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			git("diff", "--no-renames", "--numstat", "-z", "HEAD")
			git("diff", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color", "-U3", "HEAD")
		}
	})
	b.Run("bounded-bulk-list", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			diffs := computeDiffSingleRepo(gitRepoTarget{Dir: dir}, "HEAD", "", porcelain, nil)
			if len(diffs) != 2 || !diffs[0].Large || diffs[0].Patch != "" {
				b.Fatal("generated file was not deferred")
			}
		}
	})
}

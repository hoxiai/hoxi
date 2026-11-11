package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/attachment"
)

func TestEcomCLISubcommands(t *testing.T) {
	if !ShouldHandle([]string{"hoxi", "ecom", "profit"}, nil) {
		t.Fatalf("expected ShouldHandle to recognize 'hoxi ecom'")
	}

	// 1. hoxi ecom profit
	var out, errBuf bytes.Buffer
	code := Run(context.Background(), []string{
		"hoxi", "ecom", "profit",
		"--platform", "amazon",
		"--price", "39.99",
		"--cost-cny", "36",
		"--dims-cm", "30x20x10",
		"--weight-kg", "0.45",
		"--ship-rate-kg", "14.4",
		"--acos-pct", "20",
	}, nil, &out, &errBuf, "", nil)
	if code != 0 || !strings.Contains(out.String(), `"fbaSizeTier": "Large Standard"`) {
		t.Fatalf("hoxi ecom profit failed code=%d stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}

	// 2. hoxi ecom listing-check
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "listing-check",
		"--platform", "amazon",
		"--brand", "ComfyNest",
		"--title", "ComfyNest Memory Foam Ergonomic Office Chair Seat Cushion for Tailbone Support - Breathable Mesh Cover",
		"--search-terms", "seat cushion chair pad desk cushion office chair pad",
	}, nil, &out, &errBuf, "", nil)
	if code != 0 || !strings.Contains(out.String(), `"duplicateSearchTokens"`) {
		t.Fatalf("hoxi ecom listing-check failed code=%d stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}

	// 3. hoxi ecom ads-audit + .env refusal
	dir := t.TempDir()
	reportFile := filepath.Join(dir, "str.csv")
	_ = attachment.WriteDelimitedFile(reportFile, [][]string{
		{"Customer Search Term", "Impressions", "Clicks", "Spend", "7 Day Total Sales", "7 Day Total Orders (#)"},
		{"cheap plastic mat", "1200", "20", "22.00", "0.00", "0"},
	}, ',')
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "ads-audit",
		"--file", reportFile,
		"--target-acos", "25",
	}, nil, &out, &errBuf, "", nil)
	if code != 0 || !strings.Contains(out.String(), `"Add Negative Exact"`) {
		t.Fatalf("hoxi ecom ads-audit failed code=%d stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}

	// Denied .env path must fail closed with exit code 1.
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "ads-audit",
		"--file", filepath.Join(dir, ".env"),
	}, nil, &out, &errBuf, "", nil)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when attempting to read .env via hoxi ecom ads-audit")
	}

	// 4. hoxi ecom shopify-csv generate & validate
	csvOut := filepath.Join(dir, "shopify.csv")
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "shopify-csv",
		"--title", "Travel Packing Cubes",
		"--option1", "Color",
		"--variants", "Black:PC-BLK:25:100,Navy:PC-NVY:25:80",
		"--psych-99",
		"--out", csvOut,
	}, nil, &out, &errBuf, "", nil)
	if code != 0 || !strings.Contains(out.String(), `"valid": true`) {
		t.Fatalf("hoxi ecom shopify-csv generate failed code=%d stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}
}

package cli

import (
	"bytes"
	"context"
	"os"
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

	// 5. hoxi ecom listing-check 1688 7-star audit from source-1688.json + CLI flag override + validation refusals
	src1688 := filepath.Join(dir, "source-1688.json")
	_ = os.WriteFile(src1688, []byte(`{
		"sourcePlatform": "1688",
		"title": "源头厂家跨境专供食品级硅胶隔热餐垫厨房防滑沥水垫一件代发批发",
		"images": ["https://cbu01.alicdn.com/img/1.jpg", "https://cbu01.alicdn.com/img/2.jpg"],
		"hasWhiteBgImage": false,
		"attributes": [{"name":"材质","value":"硅胶"}]
	}`), 0o600)
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "listing-check",
		"--seller-7star",
		"--from-json", src1688,
	}, nil, &out, &errBuf, dir, nil)
	if code != 0 || !strings.Contains(out.String(), `"ALIBABA_1688_WHITE_BG_MISSING"`) || !strings.Contains(out.String(), `"ALIBABA_1688_IMAGE_COUNT_LOW"`) {
		t.Fatalf("expected 1688 7-star audit issues from source-1688.json, code=%d stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}

	// Explicit --white-bg true on CLI overrides hasWhiteBgImage=false in JSON.
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "listing-check",
		"--seller-7star",
		"--white-bg", "true",
		"--from-json", src1688,
	}, nil, &out, &errBuf, dir, nil)
	if code != 0 || strings.Contains(out.String(), `"ALIBABA_1688_WHITE_BG_MISSING"`) {
		t.Fatalf("expected --white-bg true CLI flag to override JSON false, code=%d stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}

	// Invalid --dims-cm must fail closed with exit code 2 (never silently drop malformed numbers).
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "listing-check",
		"--platform", "1688",
		"--title", "源头厂家跨境专供硅胶餐垫批发",
		"--dims-cm", "30xbadx10",
	}, nil, &out, &errBuf, dir, nil)
	if code != 2 {
		t.Fatalf("expected exit code 2 on malformed --dims-cm, got %d", code)
	}

	// 6. hoxi ecom listing-check also accepts hoxi-work products/<slug>/product.json (source1688 nested schema)
	workProd := filepath.Join(dir, "product.json")
	_ = os.WriteFile(workProd, []byte(`{
		"slug": "silicone-mat",
		"source1688": {
			"titleCn": "源头厂家跨境专供食品级硅胶隔热餐垫厨房防滑沥水垫一件代发批发",
			"images": [
				"https://cbu01.alicdn.com/img/1.jpg",
				"https://cbu01.alicdn.com/img/2.jpg",
				"https://cbu01.alicdn.com/img/3.jpg",
				"https://cbu01.alicdn.com/img/4.jpg",
				"https://cbu01.alicdn.com/img/5.jpg"
			],
			"attributes": {"材质":"硅胶","尺寸":"40x30","耐温":"230℃","品牌":"中性","产地":"广东","认证":"LFGB","克重":"280g","包装":"OPP袋"},
			"actualWeightG": 280,
			"dimensionsCm": {"lengthCm": 41, "widthCm": 31, "heightCm": 1.5},
			"stepPrices": [{"moq": 2, "priceRmb": 16.8}, {"moq": 100, "priceRmb": 13.9}]
		}
	}`), 0o600)
	out.Reset()
	errBuf.Reset()
	code = Run(context.Background(), []string{
		"hoxi", "ecom", "listing-check",
		"--seller-7star",
		"--white-bg", "true",
		"--has-video", "true",
		"--from-json", workProd,
	}, nil, &out, &errBuf, dir, nil)
	if code != 0 || !strings.Contains(out.String(), `"passed": true`) || !strings.Contains(out.String(), `"score": 100`) {
		t.Fatalf("expected clean 100 score on hoxi-work product.json, code=%d stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}
}

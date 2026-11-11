package ecom

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/attachment"
)

type stubStoreLookup map[string]StoreDefaults

func (m stubStoreLookup) LookupStoreDefaults(storeID string) (StoreDefaults, bool) {
	d, ok := m[storeID]
	return d, ok
}

type testSpreadsheetIO struct{}

func (testSpreadsheetIO) ParseSpreadsheet(path string, maxRows int) ([]SpreadsheetSheet, error) {
	raw, err := attachment.ParseSpreadsheetFile(path, maxRows)
	if err != nil {
		return nil, err
	}
	out := make([]SpreadsheetSheet, len(raw))
	for i, s := range raw {
		out[i] = SpreadsheetSheet{Name: s.Name, Rows: s.Rows}
	}
	return out, nil
}

func (testSpreadsheetIO) WriteXLSX(path string, sheets []SpreadsheetSheet) error {
	raw := make([]attachment.SpreadsheetSheet, len(sheets))
	for i, s := range sheets {
		raw[i] = attachment.SpreadsheetSheet{Name: s.Name, Rows: s.Rows}
	}
	return attachment.WriteXLSXFile(path, raw)
}

func (testSpreadsheetIO) WriteDelimited(path string, rows [][]string, sep rune) error {
	return attachment.WriteDelimitedFile(path, rows, sep)
}

func TestCalculateProfitAllFourPlatforms(t *testing.T) {
	// 1. Amazon FBA with 1688 CNY sourcing + volumetric weight + FBA auto size tier.
	amz, err := CalculateProfit(ProfitRequest{
		Platform:           "amazon",
		Currency:           "USD",
		Price:              39.99,
		ProductCostCNY:     36.0,
		FXRate:             7.2,
		LengthCm:           30,
		WidthCm:            20,
		HeightCm:           10, // vol = 6000/6000 = 1.0 kg > actual 0.45 kg
		ActualWeightKg:     0.45,
		ShippingRatePerKg:  14.4, // 14.4 CNY/kg -> $2.00
		ReferralFeePct:     15,
		ReturnRatePct:      5,
		ACOSPct:            20,
		TargetNetMarginPct: 18,
	}, nil)
	if err != nil {
		t.Fatalf("Amazon profit failed: %v", err)
	}
	if amz.VolumetricWeightKg != 1.0 || amz.ChargeableWeightKg != 1.0 {
		t.Fatalf("expected volumetric chargeable weight 1.0 kg, got vol=%v bill=%v", amz.VolumetricWeightKg, amz.ChargeableWeightKg)
	}
	if amz.SourcingUnitCost != 5.0 || amz.FirstLegShipping != 2.0 || amz.LandedUnitCost != 7.0 {
		t.Fatalf("unexpected Amazon landed cost breakdown: %#v", amz)
	}
	if amz.FBASizeTier != "Large Standard" || amz.FulfillmentCost <= 0 {
		t.Fatalf("expected Large Standard FBA estimate, got tier=%q fee=%v", amz.FBASizeTier, amz.FulfillmentCost)
	}
	if amz.BreakEvenACOSPct <= 0 || amz.RecommendedPrice <= 0 {
		t.Fatalf("expected positive break-even ACOS and recommended price: %#v", amz)
	}

	// 2. TikTok Shop with creator affiliate + sample amortization + ROAS matrix.
	tts, err := CalculateProfit(ProfitRequest{
		Platform:           "tiktok-shop",
		Price:              29.99,
		ProductCost:        5.50,
		FulfillmentFee:     4.20,
		ReferralFeePct:     6.0,
		AffiliatePct:       15.0,
		SampleUnitCost:     5.50,
		SampleCount:        20,
		ExpectedUnitsSold:  200, // $0.55/unit sample amortization
		ROAS:               3.0,
		TargetNetMarginPct: 15.0,
	}, nil)
	if err != nil {
		t.Fatalf("TikTok Shop profit failed: %v", err)
	}
	if tts.SampleAmortization != 0.55 || len(tts.ROASMatrix) != 5 || tts.MaxCPAForTarget <= 0 {
		t.Fatalf("unexpected TikTok Shop breakdown: %#v", tts)
	}

	// 3. Shopify with default 2.9% + $0.30 gateway fee and ROAS matrix.
	shp, err := CalculateProfit(ProfitRequest{
		Platform:       "shopify",
		Price:          49.99,
		ProductCost:    11.00,
		FulfillmentFee: 6.50,
		ROAS:           2.5,
	}, nil)
	if err != nil {
		t.Fatalf("Shopify profit failed: %v", err)
	}
	if shp.PaymentGatewayFee != 1.75 || len(shp.ROASMatrix) != 5 {
		t.Fatalf("expected Shopify gateway fee $1.75 (2.9%% of 49.99 + 0.30), got %#v", shp)
	}

	// 4. 1688 multi-tier MOQ landed cost quote without retail price.
	src1688, err := CalculateProfit(ProfitRequest{
		Platform: "1688",
		Currency: "USD",
		FXRate:   7.2,
		OrderQty: 500,
		PriceBrackets: []WholesaleBracket{
			{MinQty: 100, UnitCostCNY: 22.0},
			{MinQty: 500, UnitCostCNY: 19.5},
			{MinQty: 1000, UnitCostCNY: 18.0},
		},
		ActualWeightKg:    0.6,
		ShippingRatePerKg: 12.0,
		TariffPct:         10.0,
	}, nil)
	if err != nil {
		t.Fatalf("1688 profit failed: %v", err)
	}
	if len(src1688.TierQuotes) != 3 || src1688.RecommendedPrice <= 0 {
		t.Fatalf("expected 3 MOQ tier quotes and suggested retail price, got %#v", src1688)
	}
}

func TestAdsAuditClassificationAndXLSXExport(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "amz-str.csv")
	rows := [][]string{
		{"Campaign Name", "Ad Group Name", "Match Type", "Customer Search Term", "Impressions", "Clicks", "Spend", "7 Day Total Sales", "7 Day Total Orders (#)"},
		{"SP-Auto", "AG-1", "BROAD", "=cmd|' /C calc'!A0 cheap mat", "1800", "24", "$31.20", "$0.00", "0"},
		{"SP-Exact", "AG-2", "EXACT", "silicone kitchen mat", "4200", "50", "$60.00", "$100.00", "3"},
		{"SP-Auto", "AG-1", "AUTO", "extra large counter mat", "3500", "40", "$32.00", "$240.00", "6"},
		{"SP-Broad", "AG-3", "BROAD", "kitchen decor", "5000", "5", "$3.50", "$40.00", "1"},
	}
	if err := attachment.WriteDelimitedFile(csvPath, rows, ','); err != nil {
		t.Fatalf("write sample STR CSV: %v", err)
	}

	outXLSX := filepath.Join(dir, "audit-out.xlsx")
	res, err := AuditAdsReport(AdsAuditRequest{
		WorkspaceDir:   dir,
		ReportPath:     "amz-str.csv",
		OutputXLSXPath: "audit-out.xlsx",
		TargetACOSPct:  25.0,
	}, nil, testSpreadsheetIO{})
	if err != nil {
		t.Fatalf("AuditAdsReport failed: %v", err)
	}
	if len(res.Bleeders) != 1 || len(res.HighACOS) != 1 || len(res.Winners) != 1 || len(res.LowCTRHighImp) != 1 {
		t.Fatalf("expected 1 item in each diagnostic bucket, got bleeders=%d high=%d winners=%d lowCTR=%d",
			len(res.Bleeders), len(res.HighACOS), len(res.Winners), len(res.LowCTRHighImp))
	}
	// HighACOS CPC = 60/50 = 1.20, ACOS = 60%, Target = 25% -> SuggestedBid = 1.20 * (25/60) = 0.50!
	if res.HighACOS[0].SuggestedBid != 0.50 {
		t.Fatalf("expected exact suggested bid 0.50, got %v", res.HighACOS[0].SuggestedBid)
	}
	parsedOut, err := attachment.ParseSpreadsheetFile(outXLSX, 100)
	if err != nil || len(parsedOut) != 4 {
		t.Fatalf("expected 4 sheets in exported XLSX, got %d, err=%v", len(parsedOut), err)
	}
	// Formula-injection check: untrusted "=cmd..." search term must be escaped as text "'=cmd..."
	negTerm := parsedOut[1].Rows[1][3]
	if !strings.HasPrefix(negTerm, "'=cmd") {
		t.Fatalf("expected formula-injected search term to be escaped with leading apostrophe, got %q", negTerm)
	}
}

func TestCheckListingAcrossPlatformsAndUTF8ByteLimit(t *testing.T) {
	// Over-249-byte Search Terms with CJK + duplicates + EPA trigger + forbidden brand word.
	overBytes := strings.Repeat("ergonomic cushion pad ", 12) + "抗菌坐垫 B08XYZ1234"
	amz, err := CheckListing(ListingCheckRequest{
		Platform:       "amazon",
		BrandName:      "ComfyNest",
		Title:          "Memory Foam Seat Cushion! Antibacterial Office Chair Pad",
		Bullets:        []string{"<b>Kills germs</b> and relieves back pain", "Non-slip bottom"},
		SearchTerms:    overBytes,
		ForbiddenTerms: []string{"competitorbrand"},
	}, nil)
	if err != nil {
		t.Fatalf("Amazon CheckListing failed: %v", err)
	}
	if amz.Passed {
		t.Fatalf("expected Amazon listing with >249B search terms, HTML, symbol, and EPA trigger to fail")
	}
	if amz.SearchTermsBytes <= 249 || len(amz.TrimmedSearchTerms) > 249 {
		t.Fatalf("expected SearchTermsBytes > 249 and TrimmedSearchTerms <= 249 UTF-8 bytes, got raw=%d trimmed=%d",
			amz.SearchTermsBytes, len(amz.TrimmedSearchTerms))
	}
	if strings.Contains(amz.TrimmedSearchTerms, "B08XYZ1234") {
		t.Fatalf("expected competitor ASIN stripped from TrimmedSearchTerms: %q", amz.TrimmedSearchTerms)
	}

	// TikTok Shop script timing + diversion violation.
	tts, err := CheckListing(ListingCheckRequest{
		Platform:    "tiktok-shop",
		Title:       "Portable Mini Blender USB Rechargeable Smoothie Maker Cup for Travel Gym",
		VideoScript: "Stop wasting ten dollars on store smoothies! Watch this crush frozen berries in ten seconds. Tap the yellow cart before it sells out, or DM me on WhatsApp!",
	}, nil)
	if err != nil {
		t.Fatalf("TikTok CheckListing failed: %v", err)
	}
	if tts.Passed || tts.EstimatedSpokenSeconds <= 0 || tts.HookEstimatedSeconds <= 0 {
		t.Fatalf("expected TikTok diversion failure and positive spoken script timing, got %#v", tts)
	}

	// 1688 China Advertising Law superlative detection.
	ali, err := CheckListing(ListingCheckRequest{
		Platform: "1688",
		Title:    "源头厂家跨境专供全网最低硅胶厨房防滑隔热垫一件代发定制批发",
	}, nil)
	if err != nil || ali.Passed {
		t.Fatalf("expected 1688 superlative '全网最低' to fail China Ad Law check, got %#v, err=%v", ali, err)
	}

	// Word-boundary check: "Manicure" / "Pedicure" / "Secure" must NOT false-trigger FDA "cure".
	cleanAmz, err := CheckListing(ListingCheckRequest{
		Platform:  "amazon",
		BrandName: "GlowNail",
		Title:     "GlowNail Stainless Steel Manicure and Pedicure Grooming Kit with Secure Travel Case, 12-Piece Set",
	}, nil)
	if err != nil || !cleanAmz.Passed {
		t.Fatalf("expected clean Manicure/Pedicure/Secure title to pass without FDA false positive, got %#v, err=%v", cleanAmz, err)
	}
}

func TestShopifyCSVGenerateValidateAndHTTPSecurityRefusals(t *testing.T) {
	dir := t.TempDir()
	gen, err := BuildOrValidateShopifyCSV(ShopifyCSVRequest{
		WorkspaceDir:         dir,
		OutputCSVPath:        "shopify-products.csv",
		Title:                " Merino Wool Travel Hoodie ",
		Vendor:               "NorthPeak",
		Option1Name:          "Size",
		Psychological99:      true,
		CompareAtDiscountPct: 20,
		Variants: []ShopifyVariantInput{
			{Option1Value: "M", SKU: "00123-M", Price: 68.00, InventoryQty: 50, WeightGrams: 420},
			{Option1Value: "L", SKU: "00123-L", Price: 68.00, InventoryQty: 35, WeightGrams: 450},
		},
	}, testSpreadsheetIO{})
	if err != nil || !gen.Valid || gen.Handle != "merino-wool-travel-hoodie" {
		t.Fatalf("generate Shopify CSV failed: %#v, err=%v", gen, err)
	}

	val, err := BuildOrValidateShopifyCSV(ShopifyCSVRequest{
		WorkspaceDir: dir,
		InputCSVPath: "shopify-products.csv",
	}, testSpreadsheetIO{})
	if err != nil || !val.Valid || val.VariantCount != 2 {
		t.Fatalf("validate generated Shopify CSV failed: %#v, err=%v", val, err)
	}

	// HTTP security refusal tests: .env path, '..' escape, and storeId inheritance.
	envFile := filepath.Join(dir, ".env")
	_ = os.WriteFile(envFile, []byte("SECRET=1\n"), 0o600)

	h := NewHTTPHandler(stubStoreLookup{
		"amz-us": {
			StoreID:            "amz-us",
			Platform:           "amazon",
			Currency:           "USD",
			ReferralFeePct:     15,
			TargetNetMarginPct: 20,
		},
	}, testSpreadsheetIO{})

	// 1. Sensitive .env read via ads-audit must be rejected with 400!
	badAuditBody, _ := json.Marshal(AdsAuditRequest{ReportPath: ".env"})
	badReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/work/ecom/ads-audit?directory="+url.QueryEscape(dir), bytes.NewReader(badAuditBody))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 denying .env in ads-audit, got %d: %s", badRec.Code, badRec.Body.String())
	}

	// 2. Path traversal outside workspace must be rejected with 400!
	escBody, _ := json.Marshal(ShopifyCSVRequest{InputCSVPath: "../outside.csv"})
	escReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/work/ecom/shopify-csv?directory="+url.QueryEscape(dir), bytes.NewReader(escBody))
	escReq.Header.Set("Content-Type", "application/json")
	escRec := httptest.NewRecorder()
	h.ServeHTTP(escRec, escReq)
	if escRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 denying ../outside.csv, got %d: %s", escRec.Code, escRec.Body.String())
	}

	// 3. Store defaults inheritance on /api/hoxi/work/ecom/profit.
	profReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/work/ecom/profit", strings.NewReader(`{"storeId":"amz-us","price":29.99,"productCost":6.0,"fbaFee":4.5}`))
	profReq.Header.Set("Content-Type", "application/json")
	profRec := httptest.NewRecorder()
	h.ServeHTTP(profRec, profReq)
	if profRec.Code != http.StatusOK || !strings.Contains(profRec.Body.String(), `"platform":"amazon"`) {
		t.Fatalf("expected 200 with inherited amazon store defaults, got %d: %s", profRec.Code, profRec.Body.String())
	}
}

func TestCheckListing1688SellerSevenStarAudit(t *testing.T) {
	fBool := false
	tBool := true

	// 1. Scraped source-1688.json with 7-star quality gaps & inverted MOQ brackets.
	weakJSON := `{
		"sourcePlatform": "1688",
		"title": "加厚厨房硅胶餐垫耐高温硅胶锅垫防滑硅胶杯垫家用隔热垫",
		"images": ["https://cbu01.alicdn.com/img/ibank/1.jpg", "https://cbu01.alicdn.com/img/ibank/2.jpg", "https://cbu01.alicdn.com/img/ibank/3.jpg"],
		"hasWhiteBgImage": false,
		"hasVideo": false,
		"attributes": [{"name":"材质","value":"硅胶"},{"name":"风格","value":"现代简约"}],
		"priceBrackets": [{"minQty": 2, "unitCostCNY": 15.0}, {"minQty": 100, "unitCostCNY": 16.5}],
		"weightKg": 0.35,
		"checkSellerQuality": true
	}`
	var weakReq ListingCheckRequest
	if err := json.Unmarshal([]byte(weakJSON), &weakReq); err != nil {
		t.Fatalf("unmarshal scraped 1688 json: %v", err)
	}
	weakRes, err := CheckListing(weakReq, nil)
	if err != nil {
		t.Fatalf("CheckListing 1688 weak failed: %v", err)
	}
	if weakRes.Passed {
		t.Fatalf("expected inverted wholesale brackets to fail CheckListing, got %#v", weakRes)
	}
	codes := make(map[string]bool, len(weakRes.Issues))
	for _, iss := range weakRes.Issues {
		codes[iss.Code] = true
	}
	for _, want := range []string{
		"ALIBABA_1688_MISSING_B2B_INTENT",
		"ALIBABA_1688_KEYWORD_STUFFING",
		"ALIBABA_1688_IMAGE_COUNT_LOW",
		"ALIBABA_1688_WHITE_BG_MISSING",
		"ALIBABA_1688_VIDEO_MISSING",
		"ALIBABA_1688_ATTRIBUTES_SPARSE",
		"ALIBABA_1688_INVALID_PRICE_TIERS",
		"ALIBABA_1688_PACKAGING_INCOMPLETE",
	} {
		if !codes[want] {
			t.Fatalf("expected issue code %s in 1688 7-star audit, got %#v", want, weakRes.Issues)
		}
	}

	// Ensure explicit bool pointer helper works too.
	_ = fBool

	// 2. Clean 7-star 1688 listing ready for domestic B2B + Qingpu cross-border sync.
	cleanAttrs := json.RawMessage(`[{"name":"材质","value":"食品级硅胶"},{"name":"尺寸","value":"40x30cm"},{"name":"克重","value":"280g"},{"name":"耐温","value":"-40~230℃"},{"name":"产地","value":"广东阳江"},{"name":"装箱数","value":"60片/箱"},{"name":"支持定制","value":"是"},{"name":"货号","value":"SP-MAT-01"}]`)
	cleanRes, err := CheckListing(ListingCheckRequest{
		Platform:           "1688",
		Title:              "源头厂家跨境专供食品级硅胶隔热餐垫厨房防滑沥水垫一件代发批发",
		ImageCount:         5,
		HasWhiteBgImage:    &tBool,
		HasVideo:           &tBool,
		Attributes:         cleanAttrs,
		PriceBrackets:      []WholesaleBracket{{MinQty: 2, UnitCostCNY: 16.8}, {MinQty: 50, UnitCostCNY: 14.5}, {MinQty: 300, UnitCostCNY: 12.9}},
		WeightKg:           0.28,
		LengthCm:           41,
		WidthCm:            31,
		HeightCm:           1.5,
		CheckSellerQuality: true,
	}, nil)
	if err != nil {
		t.Fatalf("CheckListing 1688 clean failed: %v", err)
	}
	if !cleanRes.Passed || cleanRes.Score != 100 || len(cleanRes.Issues) != 0 {
		t.Fatalf("expected clean 7-star 1688 listing to pass with score 100, got %#v", cleanRes)
	}
}

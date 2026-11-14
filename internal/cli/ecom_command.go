package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/ecom"
	"github.com/hoxiai/hoxi/internal/workapps"
)

type cliSpreadsheetIO struct{}

func (cliSpreadsheetIO) ParseSpreadsheet(path string, maxRows int) ([]ecom.SpreadsheetSheet, error) {
	raw, err := attachment.ParseSpreadsheetFile(path, maxRows)
	if err != nil {
		return nil, err
	}
	out := make([]ecom.SpreadsheetSheet, len(raw))
	for i, s := range raw {
		out[i] = ecom.SpreadsheetSheet{Name: s.Name, Rows: s.Rows}
	}
	return out, nil
}

func (cliSpreadsheetIO) WriteXLSX(path string, sheets []ecom.SpreadsheetSheet) error {
	raw := make([]attachment.SpreadsheetSheet, len(sheets))
	for i, s := range sheets {
		raw[i] = attachment.SpreadsheetSheet{Name: s.Name, Rows: s.Rows}
	}
	return attachment.WriteXLSXFile(path, raw)
}

func (cliSpreadsheetIO) WriteDelimited(path string, rows [][]string, sep rune) error {
	return attachment.WriteDelimitedFile(path, rows, sep)
}

type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, " | ") }
func (s *stringListFlag) Set(val string) error {
	*s = append(*s, val)
	return nil
}

func loadCLIStoreDefaults(storeID string) *ecom.StoreDefaults {
	storeID = strings.TrimSpace(storeID)
	if storeID == "" {
		return nil
	}
	svc := workapps.NewService(nil, workapps.NewStore(""))
	prof, ok := svc.GetStore(storeID)
	if !ok || prof == nil {
		return nil
	}
	return &ecom.StoreDefaults{
		StoreID:            prof.ID,
		Platform:           prof.Platform,
		Marketplace:        prof.Marketplace,
		Currency:           prof.Currency,
		BrandName:          prof.BrandName,
		ReferralFeePct:     prof.ReferralFeePct,
		TargetNetMarginPct: prof.TargetNetMarginPct,
		ShippingRatePerKg:  prof.ShippingRatePerKg,
		ShippingCurrency:   prof.ShippingCurrency,
		FXRate:             prof.FXRate,
		ReturnRatePct:      prof.ReturnRatePct,
		TargetACOSPct:      prof.TargetACOSPct,
		TargetROAS:         prof.TargetROAS,
		ForbiddenTerms:     prof.ForbiddenTerms,
	}
}

func runEcomCommand(args []string, cwd string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printEcomHelp(stdout)
		return 0
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]
	switch action {
	case "help", "-h", "--help":
		printEcomHelp(stdout)
		return 0
	case "profit":
		return runEcomProfit(rest, stdout, stderr)
	case "ads-audit":
		return runEcomAdsAudit(rest, cwd, stdout, stderr)
	case "listing-check":
		return runEcomListingCheck(rest, cwd, stdout, stderr)
	case "shopify-csv":
		return runEcomShopifyCSV(rest, cwd, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unknown `hoxi ecom` subcommand %q (expected profit | ads-audit | listing-check | shopify-csv)\n", action)
		return 2
	}
}

func runEcomProfit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi ecom profit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		req      ecom.ProfitRequest
		dims     string
		brackets string
		asJSON   bool
	)
	fs.StringVar(&req.StoreID, "store", "", "bound store ID to inherit fee/FX/margin defaults")
	fs.StringVar(&req.Platform, "platform", "", "platform: amazon | tiktok-shop | shopify | 1688")
	fs.StringVar(&req.Currency, "currency", "", "selling currency (e.g. USD, GBP, EUR, CNY)")
	fs.Float64Var(&req.Price, "price", 0, "retail unit price")
	fs.IntVar(&req.OrderQty, "qty", 100, "order quantity for 1688 brackets")
	fs.Float64Var(&req.ProductCost, "cost", 0, "unit sourcing cost in selling currency")
	fs.Float64Var(&req.ProductCostCNY, "cost-cny", 0, "unit sourcing cost in CNY")
	fs.StringVar(&brackets, "brackets", "", "1688 wholesale brackets 'minQty:cny,...' (e.g. 100:22,500:19.5,1000:18)")
	fs.Float64Var(&req.DomesticShipCNY, "domestic-ship-cny", 0, "1688 domestic China freight per unit (CNY)")
	fs.Float64Var(&req.ActualWeightKg, "weight-kg", 0, "unit actual weight in kg")
	fs.StringVar(&dims, "dims-cm", "", "package dimensions LxWxH in cm (e.g. 30x20x10)")
	fs.Float64Var(&req.ShippingRatePerKg, "ship-rate-kg", 0, "first-leg shipping rate per chargeable kg")
	fs.Float64Var(&req.ShippingCost, "ship-cost", 0, "explicit first-leg shipping cost per unit")
	fs.Float64Var(&req.FXRate, "fx", 0, "CNY per 1 unit of selling currency (default 7.20)")
	fs.Float64Var(&req.TariffPct, "tariff-pct", 0, "import tariff/duty %")
	fs.Float64Var(&req.PrepFee, "prep-fee", 0, "prep/labeling fee per unit")
	fs.Float64Var(&req.ReferralFeePct, "referral-pct", 0, "platform referral/commission %")
	fs.Float64Var(&req.FBAFee, "fba-fee", 0, "explicit Amazon FBA fulfillment fee")
	fs.Float64Var(&req.FulfillmentFee, "fulfillment-fee", 0, "3PL/TTS fulfillment pick-pack-ship fee")
	fs.Float64Var(&req.StorageFee, "storage-fee", 0, "monthly storage fee per unit")
	fs.Float64Var(&req.AffiliatePct, "affiliate-pct", 0, "TikTok Shop creator affiliate commission %")
	fs.Float64Var(&req.SampleUnitCost, "sample-cost", 0, "creator sample unit cost")
	fs.IntVar(&req.SampleCount, "sample-count", 0, "number of creator samples sent")
	fs.IntVar(&req.ExpectedUnitsSold, "expected-units", 0, "expected units sold to amortize samples")
	fs.Float64Var(&req.ShippingSubsidy, "shipping-subsidy", 0, "seller shipping subsidy per order")
	fs.Float64Var(&req.GatewayFeePct, "gateway-pct", 0, "Shopify/payment gateway %")
	fs.Float64Var(&req.GatewayFixedFee, "gateway-fixed", 0, "Shopify/payment gateway fixed fee")
	fs.Float64Var(&req.ReturnRatePct, "return-pct", 0, "expected return/refund rate %")
	fs.Float64Var(&req.ACOSPct, "acos-pct", 0, "expected ad ACOS %")
	fs.Float64Var(&req.ROAS, "roas", 0, "expected ad ROAS multiple")
	fs.Float64Var(&req.AdSpendPerUnit, "ad-spend", 0, "explicit ad CPA per unit")
	fs.Float64Var(&req.TargetNetMarginPct, "target-margin-pct", 0, "target net margin %")
	fs.BoolVar(&asJSON, "json", true, "emit structured JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if dims != "" {
		l, w, h, dErr := parseDimsCmFlag(dims)
		if dErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", dErr)
			return 2
		}
		req.LengthCm, req.WidthCm, req.HeightCm = l, w, h
	}
	if brackets != "" {
		parsedBrackets, bErr := parseWholesaleBracketsFlag(brackets)
		if bErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", bErr)
			return 2
		}
		req.PriceBrackets = parsedBrackets
	}
	res, err := ecom.CalculateProfit(req, loadCLIStoreDefaults(req.StoreID))
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	writeCLIJSON(stdout, res)
	return 0
}

func runEcomAdsAudit(args []string, cwd string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi ecom ads-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		req     ecom.AdsAuditRequest
		fileArg string
		outArg  string
		dirArg  string
	)
	fs.StringVar(&req.StoreID, "store", "", "bound store ID")
	fs.StringVar(&fileArg, "file", "", "ad report spreadsheet (.xlsx, .xls, .ods, .csv, .tsv)")
	fs.StringVar(&fileArg, "f", "", "shorthand for --file")
	fs.StringVar(&outArg, "out", "", "optional output .xlsx workbook path")
	fs.StringVar(&outArg, "o", "", "shorthand for --out")
	fs.StringVar(&dirArg, "dir", cwd, "workspace base directory")
	fs.Float64Var(&req.TargetACOSPct, "target-acos", 0, "target ACOS % (default 25)")
	fs.Float64Var(&req.WasteSpendThreshold, "waste-spend", 0, "0-order bleed threshold spend (default 15)")
	fs.IntVar(&req.WasteClickThreshold, "waste-clicks", 0, "0-order bleed threshold clicks (default 12)")
	fs.IntVar(&req.WinnerMinOrders, "winner-orders", 0, "minimum orders for harvest winner (default 2)")
	fs.IntVar(&req.MaxItemsPerBucket, "max-items", 100, "max rows per bucket")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fileArg == "" && fs.NArg() > 0 {
		fileArg = fs.Arg(0)
	}
	safeReport, err := resolveSafeDocPath(fileArg, dirArg)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	req.ReportPath = safeReport
	if strings.TrimSpace(outArg) != "" {
		safeOut, outErr := resolveSafeDocPath(outArg, dirArg)
		if outErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", outErr)
			return 1
		}
		req.OutputXLSXPath = safeOut
	}
	res, err := ecom.AuditAdsReport(req, loadCLIStoreDefaults(req.StoreID), cliSpreadsheetIO{})
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	writeCLIJSON(stdout, res)
	return 0
}

func runEcomListingCheck(args []string, cwd string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi ecom listing-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		cliReq    ecom.ListingCheckRequest
		bullets   stringListFlag
		forbidden string
		fromJSON  string
		whiteBg   string
		hasVideo  string
		brackets  string
		dims      string
		strict    bool
	)
	fs.StringVar(&cliReq.StoreID, "store", "", "bound store ID")
	fs.StringVar(&cliReq.Platform, "platform", "", "platform: amazon | tiktok-shop | shopify | 1688")
	fs.StringVar(&cliReq.BrandName, "brand", "", "brand name")
	fs.StringVar(&cliReq.Title, "title", "", "product listing title")
	fs.Var(&bullets, "bullet", "bullet point (repeatable)")
	fs.StringVar(&cliReq.Description, "description", "", "product description")
	fs.StringVar(&cliReq.SearchTerms, "search-terms", "", "Amazon backend Search Terms (checked against 249 UTF-8 byte cap)")
	fs.StringVar(&cliReq.VideoScript, "script", "", "TikTok Shop short-video spoken script")
	fs.StringVar(&cliReq.MetaTitle, "meta-title", "", "Shopify SEO title")
	fs.StringVar(&cliReq.MetaDescription, "meta-description", "", "Shopify SEO meta description")
	fs.StringVar(&cliReq.Handle, "handle", "", "Shopify URL handle")
	fs.StringVar(&forbidden, "forbidden", "", "comma-separated forbidden words")
	fs.IntVar(&cliReq.ImageCount, "images", 0, "1688 main gallery image count")
	fs.StringVar(&whiteBg, "white-bg", "", "1688 5th pure-white-bg image present: true | false")
	fs.StringVar(&hasVideo, "has-video", "", "1688 main gallery video present: true | false")
	fs.IntVar(&cliReq.AttributeCount, "attr-count", 0, "1688 filled product attribute count")
	fs.StringVar(&brackets, "brackets", "", "1688 wholesale brackets 'minQty:cny,...'")
	fs.Float64Var(&cliReq.WeightKg, "weight-kg", 0, "1688 cross-border unit weight in kg")
	fs.StringVar(&dims, "dims-cm", "", "1688 cross-border package dimensions LxWxH in cm")
	fs.BoolVar(&cliReq.CheckSellerQuality, "seller-7star", false, "run full 1688 7-star seller quality & cross-border readiness audit")
	fs.StringVar(&fromJSON, "from-json", "", "optional JSON file containing ListingCheckRequest or scraped source-1688.json")
	fs.BoolVar(&strict, "strict", false, "exit 1 when compliance errors are detected")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var req ecom.ListingCheckRequest
	if strings.TrimSpace(fromJSON) != "" {
		safeJSON, err := resolveSafeDocPath(fromJSON, cwd)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		raw, err := os.ReadFile(safeJSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error reading --from-json: %v\n", err)
			return 1
		}
		if err := unmarshalListingCheckJSON(raw, &req); err != nil {
			fmt.Fprintf(stderr, "Error parsing --from-json: %v\n", err)
			return 1
		}
	}
	setFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	if setFlags["store"] {
		req.StoreID = cliReq.StoreID
	}
	if setFlags["platform"] {
		req.Platform = cliReq.Platform
	}
	if setFlags["brand"] {
		req.BrandName = cliReq.BrandName
	}
	if setFlags["title"] {
		req.Title = cliReq.Title
	}
	if setFlags["description"] {
		req.Description = cliReq.Description
	}
	if setFlags["search-terms"] {
		req.SearchTerms = cliReq.SearchTerms
	}
	if setFlags["script"] {
		req.VideoScript = cliReq.VideoScript
	}
	if setFlags["meta-title"] {
		req.MetaTitle = cliReq.MetaTitle
	}
	if setFlags["meta-description"] {
		req.MetaDescription = cliReq.MetaDescription
	}
	if setFlags["handle"] {
		req.Handle = cliReq.Handle
	}
	if setFlags["images"] {
		req.ImageCount = cliReq.ImageCount
	}
	if setFlags["attr-count"] {
		req.AttributeCount = cliReq.AttributeCount
	}
	if setFlags["weight-kg"] {
		req.WeightKg = cliReq.WeightKg
	}
	if setFlags["seller-7star"] {
		req.CheckSellerQuality = cliReq.CheckSellerQuality
	}
	if whiteBg != "" {
		b, bErr := parseOptionalBoolFlag("--white-bg", whiteBg)
		if bErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", bErr)
			return 2
		}
		req.HasWhiteBgImage = b
	}
	if hasVideo != "" {
		b, bErr := parseOptionalBoolFlag("--has-video", hasVideo)
		if bErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", bErr)
			return 2
		}
		req.HasVideo = b
	}
	if dims != "" {
		l, w, h, dErr := parseDimsCmFlag(dims)
		if dErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", dErr)
			return 2
		}
		req.LengthCm, req.WidthCm, req.HeightCm = l, w, h
	}
	if brackets != "" {
		parsedBrackets, bErr := parseWholesaleBracketsFlag(brackets)
		if bErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", bErr)
			return 2
		}
		req.PriceBrackets = parsedBrackets
	}
	if len(bullets) > 0 {
		req.Bullets = append(req.Bullets, bullets...)
	}
	if strings.TrimSpace(forbidden) != "" {
		for _, f := range strings.Split(forbidden, ",") {
			if t := strings.TrimSpace(f); t != "" {
				req.ForbiddenTerms = append(req.ForbiddenTerms, t)
			}
		}
	}
	res, err := ecom.CheckListing(req, loadCLIStoreDefaults(req.StoreID))
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	writeCLIJSON(stdout, res)
	if strict && !res.Passed {
		return 1
	}
	return 0
}

func runEcomShopifyCSV(args []string, cwd string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi ecom shopify-csv", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		req          ecom.ShopifyCSVRequest
		dirArg       string
		inArg        string
		outArg       string
		fromJSON     string
		tagsRaw      string
		variantsSpec string
	)
	fs.StringVar(&dirArg, "dir", cwd, "workspace directory")
	fs.StringVar(&inArg, "in", "", "existing Shopify CSV file to validate")
	fs.StringVar(&outArg, "out", "", "output Shopify CSV path to generate")
	fs.StringVar(&outArg, "o", "", "shorthand for --out")
	fs.StringVar(&fromJSON, "from-json", "", "JSON file describing ShopifyCSVRequest")
	fs.StringVar(&req.Title, "title", "", "product title")
	fs.StringVar(&req.Handle, "handle", "", "URL handle")
	fs.StringVar(&req.Vendor, "vendor", "", "brand/vendor")
	fs.StringVar(&req.ProductType, "type", "", "product type")
	fs.StringVar(&tagsRaw, "tags", "", "comma-separated tags")
	fs.StringVar(&req.Option1Name, "option1", "", "Option1 name (e.g. Size, Color)")
	fs.BoolVar(&req.Psychological99, "psych-99", false, "round variant prices to .99")
	fs.Float64Var(&req.CompareAtDiscountPct, "compare-discount-pct", 0, "strikethrough discount % (e.g. 20)")
	fs.StringVar(&variantsSpec, "variants", "", "compact variants 'opt1:sku:price:qty,...'")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(fromJSON) != "" {
		safeJSON, err := resolveSafeDocPath(fromJSON, dirArg)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		raw, err := os.ReadFile(safeJSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error reading --from-json: %v\n", err)
			return 1
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			fmt.Fprintf(stderr, "Error parsing --from-json: %v\n", err)
			return 1
		}
	}
	if strings.TrimSpace(inArg) != "" {
		safeIn, err := resolveSafeDocPath(inArg, dirArg)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		req.InputCSVPath = safeIn
	}
	if strings.TrimSpace(outArg) != "" {
		safeOut, err := resolveSafeDocPath(outArg, dirArg)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		req.OutputCSVPath = safeOut
	}
	if strings.TrimSpace(tagsRaw) != "" {
		for _, t := range strings.Split(tagsRaw, ",") {
			if s := strings.TrimSpace(t); s != "" {
				req.Tags = append(req.Tags, s)
			}
		}
	}
	if strings.TrimSpace(variantsSpec) != "" {
		for _, seg := range strings.Split(variantsSpec, ",") {
			parts := strings.Split(strings.TrimSpace(seg), ":")
			if len(parts) < 3 {
				fmt.Fprintf(stderr, "Error: invalid --variants item %q (expected opt1:sku:price[:qty])\n", seg)
				return 2
			}
			p, pErr := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
			if pErr != nil {
				fmt.Fprintf(stderr, "Error: invalid variant price in %q\n", seg)
				return 2
			}
			qty := 50
			if len(parts) >= 4 {
				qty, _ = strconv.Atoi(strings.TrimSpace(parts[3]))
			}
			req.Variants = append(req.Variants, ecom.ShopifyVariantInput{
				Option1Value: strings.TrimSpace(parts[0]),
				SKU:          strings.TrimSpace(parts[1]),
				Price:        p,
				InventoryQty: qty,
			})
		}
	}
	res, err := ecom.BuildOrValidateShopifyCSV(req, cliSpreadsheetIO{})
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	writeCLIJSON(stdout, res)
	return 0
}

func parseDimsCmFlag(dims string) (float64, float64, float64, error) {
	parts := strings.FieldsFunc(strings.ToLower(dims), func(r rune) bool { return r == 'x' || r == '*' || r == ',' })
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("--dims-cm must be formatted as LxWxH (e.g. 30x20x10)")
	}
	l, lErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	w, wErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	h, hErr := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if lErr != nil || wErr != nil || hErr != nil || l <= 0 || w <= 0 || h <= 0 {
		return 0, 0, 0, fmt.Errorf("invalid --dims-cm %q (expected positive LxWxH numbers in cm)", dims)
	}
	return l, w, h, nil
}

func parseWholesaleBracketsFlag(raw string) ([]ecom.WholesaleBracket, error) {
	var out []ecom.WholesaleBracket
	for _, seg := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(seg), ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid --brackets entry %q (expected minQty:unitCostCNY)", seg)
		}
		q, qErr := strconv.Atoi(strings.TrimSpace(kv[0]))
		c, cErr := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
		if qErr != nil || cErr != nil || q <= 0 || c <= 0 {
			return nil, fmt.Errorf("invalid --brackets entry %q", seg)
		}
		out = append(out, ecom.WholesaleBracket{MinQty: q, UnitCostCNY: c})
	}
	return out, nil
}

func parseOptionalBoolFlag(flagName, raw string) (*bool, error) {
	b, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid %s value %q (expected true or false)", flagName, raw)
	}
	return &b, nil
}

type workProductJSONFallback struct {
	TitleCn    string  `json:"titleCn"`
	WeightG    float64 `json:"weightG"`
	DimsCm     string  `json:"dimsCm"`
	StepPrices []struct {
		MOQ      int     `json:"moq"`
		PriceRmb float64 `json:"priceRmb"`
	} `json:"stepPrices"`
	Source1688 *struct {
		TitleCn           string          `json:"titleCn"`
		Images            []string        `json:"images"`
		Attributes        json.RawMessage `json:"attributes"`
		ActualWeightG     float64         `json:"actualWeightG"`
		ChargeableWeightG float64         `json:"chargeableWeightG"`
		DimsCm            string          `json:"dimsCm"`
		DimensionsCm      *struct {
			LengthCm float64 `json:"lengthCm"`
			WidthCm  float64 `json:"widthCm"`
			HeightCm float64 `json:"heightCm"`
		} `json:"dimensionsCm"`
		StepPrices []struct {
			MOQ      int     `json:"moq"`
			PriceRmb float64 `json:"priceRmb"`
		} `json:"stepPrices"`
	} `json:"source1688"`
}

func unmarshalListingCheckJSON(raw []byte, req *ecom.ListingCheckRequest) error {
	if err := json.Unmarshal(raw, req); err != nil {
		return err
	}
	var fb workProductJSONFallback
	if err := json.Unmarshal(raw, &fb); err != nil {
		return err
	}
	if strings.TrimSpace(req.Platform) == "" && strings.TrimSpace(req.SourcePlatform) == "" && fb.Source1688 != nil {
		req.SourcePlatform = "1688"
	}
	if strings.TrimSpace(req.Title) == "" {
		if fb.TitleCn != "" {
			req.Title = fb.TitleCn
		} else if fb.Source1688 != nil && fb.Source1688.TitleCn != "" {
			req.Title = fb.Source1688.TitleCn
		}
	}
	if len(req.Images) == 0 && fb.Source1688 != nil && len(fb.Source1688.Images) > 0 {
		req.Images = fb.Source1688.Images
	}
	if len(req.Attributes) == 0 && fb.Source1688 != nil && len(fb.Source1688.Attributes) > 0 {
		req.Attributes = fb.Source1688.Attributes
	}
	if len(req.PriceBrackets) == 0 {
		steps := fb.StepPrices
		if len(steps) == 0 && fb.Source1688 != nil {
			steps = fb.Source1688.StepPrices
		}
		for _, sp := range steps {
			if sp.MOQ > 0 && sp.PriceRmb > 0 {
				req.PriceBrackets = append(req.PriceBrackets, ecom.WholesaleBracket{MinQty: sp.MOQ, UnitCostCNY: sp.PriceRmb})
			}
		}
	}
	if req.WeightKg <= 0 {
		if fb.WeightG > 0 {
			req.WeightKg = fb.WeightG / 1000
		} else if fb.Source1688 != nil && fb.Source1688.ActualWeightG > 0 {
			req.WeightKg = fb.Source1688.ActualWeightG / 1000
		} else if fb.Source1688 != nil && fb.Source1688.ChargeableWeightG > 0 {
			req.WeightKg = fb.Source1688.ChargeableWeightG / 1000
		}
	}
	if req.LengthCm <= 0 && req.WidthCm <= 0 && req.HeightCm <= 0 {
		if fb.Source1688 != nil && fb.Source1688.DimensionsCm != nil {
			req.LengthCm = fb.Source1688.DimensionsCm.LengthCm
			req.WidthCm = fb.Source1688.DimensionsCm.WidthCm
			req.HeightCm = fb.Source1688.DimensionsCm.HeightCm
		} else {
			dimsStr := strings.TrimSpace(fb.DimsCm)
			if dimsStr == "" && fb.Source1688 != nil {
				dimsStr = strings.TrimSpace(fb.Source1688.DimsCm)
			}
			if dimsStr != "" {
				l, w, h, err := parseDimsCmFlag(dimsStr)
				if err != nil {
					return err
				}
				req.LengthCm, req.WidthCm, req.HeightCm = l, w, h
			}
		}
	}
	return nil
}

func writeCLIJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func printEcomHelp(w io.Writer) {
	fmt.Fprint(w, `Hoxi E-Commerce Engine (hoxi ecom) — Deterministic Profit, Ad Audit, Compliance & Catalog CLI

Usage:
  hoxi ecom profit [flags]          Exact unit-economics, FBA tier, 1688 volumetric landed cost & ROAS matrix
  hoxi ecom ads-audit [flags]       Diagnose Amazon/TikTok/Google Search Term reports & export negative/bid .xlsx
  hoxi ecom listing-check [flags]   Check Amazon 249B UTF-8 search terms, EPA/FDA triggers, TikTok 3s hook & 1688 7-star quality
  hoxi ecom shopify-csv [flags]     Generate or validate multi-variant Shopify product import CSVs

Examples:
  hoxi ecom profit --platform amazon --price 39.99 --cost-cny 36 --dims-cm 30x20x10 --weight-kg 0.45 --ship-rate-kg 14 --acos-pct 20
  hoxi ecom profit --platform tiktok-shop --price 29.99 --cost 5.5 --fulfillment-fee 4.2 --affiliate-pct 15 --roas 3
  hoxi ecom profit --platform 1688 --brackets 100:22,500:19.5,1000:18 --weight-kg 0.6 --ship-rate-kg 12 --fx 7.2
  hoxi ecom ads-audit --file ./SearchTermReport.xlsx --target-acos 25 --out ./ads-action-plan.xlsx
  hoxi ecom listing-check --platform amazon --brand ComfyNest --title "ComfyNest Ergonomic Seat Cushion..." --search-terms "..."
  hoxi ecom listing-check --platform 1688 --seller-7star --from-json ./products/881234/source-1688.json
  hoxi ecom shopify-csv --title "Merino Hoodie" --option1 Size --variants "M:HD-M:68:50,L:HD-L:68:35" --psych-99 --out ./shopify.csv
`)
}

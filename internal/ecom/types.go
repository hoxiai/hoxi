package ecom

// StoreDefaults carries store-level unit economics and compliance baselines
// inherited automatically when a CLI command or HTTP request specifies storeId.
type StoreDefaults struct {
	StoreID            string   `json:"storeId,omitempty"`
	Platform           string   `json:"platform,omitempty"`
	Marketplace        string   `json:"marketplace,omitempty"`
	Currency           string   `json:"currency,omitempty"`
	BrandName          string   `json:"brandName,omitempty"`
	ReferralFeePct     float64  `json:"referralFeePct,omitempty"`
	TargetNetMarginPct float64  `json:"targetNetMarginPct,omitempty"`
	ShippingRatePerKg  float64  `json:"shippingRatePerKg,omitempty"`
	ShippingCurrency   string   `json:"shippingCurrency,omitempty"`
	FXRate             float64  `json:"fxRate,omitempty"`
	ReturnRatePct      float64  `json:"returnRatePct,omitempty"`
	TargetACOSPct      float64  `json:"targetACOSPct,omitempty"`
	TargetROAS         float64  `json:"targetROAS,omitempty"`
	ForbiddenTerms     []string `json:"forbiddenTerms,omitempty"`
}

// StoreLookup resolves optional store defaults by store ID without coupling
// internal/ecom to internal/workapps.
type StoreLookup interface {
	LookupStoreDefaults(storeID string) (StoreDefaults, bool)
}

// SpreadsheetSheet holds parsed or generated tabular rows without coupling
// internal/ecom to internal/attachment.
type SpreadsheetSheet struct {
	Name string
	Rows [][]string
}

// SpreadsheetIO abstracts tabular file parsing and export (.xlsx / .csv / .tsv)
// injected by top-level wiring packages (internal/gateway or internal/cli).
type SpreadsheetIO interface {
	ParseSpreadsheet(path string, maxRows int) ([]SpreadsheetSheet, error)
	WriteXLSX(path string, sheets []SpreadsheetSheet) error
	WriteDelimited(path string, rows [][]string, sep rune) error
}

// WholesaleBracket represents a 1688 quantity price break (e.g. >=100 ¥18.5, >=500 ¥16.8).
type WholesaleBracket struct {
	MinQty      int     `json:"minQty"`
	UnitCostCNY float64 `json:"unitCostCNY"`
}

// ProfitRequest defines deterministic unit-economics inputs across Amazon,
// TikTok Shop, Shopify, and 1688 sourcing.
type ProfitRequest struct {
	StoreID     string `json:"storeId,omitempty"`
	Platform    string `json:"platform"`
	Marketplace string `json:"marketplace,omitempty"`
	Currency    string `json:"currency,omitempty"`

	// Selling price & order quantity.
	Price    float64 `json:"price"`
	OrderQty int     `json:"orderQty,omitempty"`

	// Sourcing / 1688 landed cost inputs.
	ProductCost       float64            `json:"productCost,omitempty"`
	ProductCostCNY    float64            `json:"productCostCNY,omitempty"`
	PriceBrackets     []WholesaleBracket `json:"priceBrackets,omitempty"`
	DomesticShipCNY   float64            `json:"domesticShipCNY,omitempty"`
	ActualWeightKg    float64            `json:"actualWeightKg,omitempty"`
	LengthCm          float64            `json:"lengthCm,omitempty"`
	WidthCm           float64            `json:"widthCm,omitempty"`
	HeightCm          float64            `json:"heightCm,omitempty"`
	VolumetricDivisor float64            `json:"volumetricDivisor,omitempty"`
	ShippingRatePerKg float64            `json:"shippingRatePerKg,omitempty"`
	ShippingCost      float64            `json:"shippingCost,omitempty"`
	FXRate            float64            `json:"fxRate,omitempty"`
	TariffPct         float64            `json:"tariffPct,omitempty"`
	PrepFee           float64            `json:"prepFee,omitempty"`

	// Platform, fulfillment, affiliate, payment & return fees.
	ReferralFeePct    float64 `json:"referralFeePct,omitempty"`
	MinReferralFee    float64 `json:"minReferralFee,omitempty"`
	FBAFee            float64 `json:"fbaFee,omitempty"`
	FulfillmentFee    float64 `json:"fulfillmentFee,omitempty"`
	StorageFee        float64 `json:"storageFee,omitempty"`
	AffiliatePct      float64 `json:"affiliatePct,omitempty"`
	SampleUnitCost    float64 `json:"sampleUnitCost,omitempty"`
	SampleCount       int     `json:"sampleCount,omitempty"`
	ExpectedUnitsSold int     `json:"expectedUnitsSold,omitempty"`
	ShippingSubsidy   float64 `json:"shippingSubsidy,omitempty"`
	GatewayFeePct     float64 `json:"gatewayFeePct,omitempty"`
	GatewayFixedFee   float64 `json:"gatewayFixedFee,omitempty"`
	PlatformTxFeePct  float64 `json:"platformTxFeePct,omitempty"`
	ReturnRatePct     float64 `json:"returnRatePct,omitempty"`

	// Advertising & target profitability.
	ACOSPct            float64 `json:"acosPct,omitempty"`
	ROAS               float64 `json:"roas,omitempty"`
	AdSpendPerUnit     float64 `json:"adSpendPerUnit,omitempty"`
	TargetNetMarginPct float64 `json:"targetNetMarginPct,omitempty"`
}

// ROASSimulationRow shows net profit and margin at a specific ROAS multiple.
type ROASSimulationRow struct {
	ROAS         float64 `json:"roas"`
	AdCPA        float64 `json:"adCPA"`
	NetProfit    float64 `json:"netProfit"`
	NetMarginPct float64 `json:"netMarginPct"`
}

// TierQuoteRow summarizes 1688 landed cost at a specific MOQ bracket.
type TierQuoteRow struct {
	Qty                int     `json:"qty"`
	UnitCostCNY        float64 `json:"unitCostCNY"`
	FirstLegShipCNY    float64 `json:"firstLegShipCNY"`
	LandedCostCNY      float64 `json:"landedCostCNY"`
	LandedCostCurrency float64 `json:"landedCostCurrency"`
	TotalBatchCNY      float64 `json:"totalBatchCNY"`
}

// ProfitBreakdown returns exact, deterministic fee and margin metrics.
type ProfitBreakdown struct {
	Platform           string              `json:"platform"`
	Currency           string              `json:"currency"`
	Price              float64             `json:"price"`
	VolumetricWeightKg float64             `json:"volumetricWeightKg,omitempty"`
	ChargeableWeightKg float64             `json:"chargeableWeightKg,omitempty"`
	FBASizeTier        string              `json:"fbaSizeTier,omitempty"`
	SourcingUnitCost   float64             `json:"sourcingUnitCost"`
	FirstLegShipping   float64             `json:"firstLegShipping"`
	TariffAndPrep      float64             `json:"tariffAndPrep"`
	LandedUnitCost     float64             `json:"landedUnitCost"`
	PlatformFee        float64             `json:"platformFee"`
	AffiliateFee       float64             `json:"affiliateFee,omitempty"`
	PaymentGatewayFee  float64             `json:"paymentGatewayFee,omitempty"`
	FulfillmentCost    float64             `json:"fulfillmentCost"`
	SampleAmortization float64             `json:"sampleAmortization,omitempty"`
	ReturnLossBuffer   float64             `json:"returnLossBuffer"`
	ProfitBeforeAds    float64             `json:"profitBeforeAds"`
	GrossMarginPct     float64             `json:"grossMarginPct"`
	AdCostPerUnit      float64             `json:"adCostPerUnit"`
	NetProfit          float64             `json:"netProfit"`
	NetMarginPct       float64             `json:"netMarginPct"`
	UnitROIPct         float64             `json:"unitRoiPct"`
	BreakEvenACOSPct   float64             `json:"breakEvenAcosPct,omitempty"`
	BreakEvenROAS      float64             `json:"breakEvenRoas,omitempty"`
	MaxCPAForTarget    float64             `json:"maxCpaForTarget,omitempty"`
	TargetNetMarginPct float64             `json:"targetNetMarginPct"`
	RecommendedPrice   float64             `json:"recommendedPrice,omitempty"`
	ROASMatrix         []ROASSimulationRow `json:"roasMatrix,omitempty"`
	TierQuotes         []TierQuoteRow      `json:"tierQuotes,omitempty"`
	Warnings           []string            `json:"warnings,omitempty"`
}

// AdsAuditRequest configures deterministic search-term / ad-report auditing.
type AdsAuditRequest struct {
	StoreID             string  `json:"storeId,omitempty"`
	WorkspaceDir        string  `json:"directory,omitempty"`
	ReportPath          string  `json:"reportPath"`
	OutputXLSXPath      string  `json:"outputXlsxPath,omitempty"`
	TargetACOSPct       float64 `json:"targetAcosPct,omitempty"`
	WasteSpendThreshold float64 `json:"wasteSpendThreshold,omitempty"`
	WasteClickThreshold int     `json:"wasteClickThreshold,omitempty"`
	WinnerMinOrders     int     `json:"winnerMinOrders,omitempty"`
	MaxItemsPerBucket   int     `json:"maxItemsPerBucket,omitempty"`
}

// AdsAuditRow represents an aggregated search term / ad target diagnostic entry.
type AdsAuditRow struct {
	SearchTerm   string  `json:"searchTerm"`
	Campaign     string  `json:"campaign,omitempty"`
	AdGroup      string  `json:"adGroup,omitempty"`
	MatchType    string  `json:"matchType,omitempty"`
	Impressions  int     `json:"impressions"`
	Clicks       int     `json:"clicks"`
	Spend        float64 `json:"spend"`
	Sales        float64 `json:"sales"`
	Orders       int     `json:"orders"`
	CTRPct       float64 `json:"ctrPct"`
	CVRPct       float64 `json:"cvrPct"`
	CPC          float64 `json:"cpc"`
	ACOSPct      float64 `json:"acosPct,omitempty"`
	ROAS         float64 `json:"roas,omitempty"`
	SuggestedBid float64 `json:"suggestedBid,omitempty"`
	Action       string  `json:"action"`
}

// AdsAuditSummary summarizes report-level ad spend efficiency.
type AdsAuditSummary struct {
	RowsParsed          int     `json:"rowsParsed"`
	UniqueTerms         int     `json:"uniqueTerms"`
	TotalImpressions    int     `json:"totalImpressions"`
	TotalClicks         int     `json:"totalClicks"`
	TotalSpend          float64 `json:"totalSpend"`
	TotalSales          float64 `json:"totalSales"`
	TotalOrders         int     `json:"totalOrders"`
	OverallCTRPct       float64 `json:"overallCtrPct"`
	OverallCVRPct       float64 `json:"overallCvrPct"`
	OverallCPC          float64 `json:"overallCpc"`
	OverallACOSPct      float64 `json:"overallAcosPct"`
	OverallROAS         float64 `json:"overallRoas"`
	TargetACOSPct       float64 `json:"targetAcosPct"`
	WastedSpend         float64 `json:"wastedSpend"`
	WastedSpendSharePct float64 `json:"wastedSpendSharePct"`
}

// AdsAuditResult holds the categorized ad report diagnosis and optional export path.
type AdsAuditResult struct {
	Summary        AdsAuditSummary `json:"summary"`
	Bleeders       []AdsAuditRow   `json:"bleeders"`
	HighACOS       []AdsAuditRow   `json:"highAcos"`
	Winners        []AdsAuditRow   `json:"winners"`
	LowCTRHighImp  []AdsAuditRow   `json:"lowCtrHighImp"`
	OutputXLSXPath string          `json:"outputXlsxPath,omitempty"`
}

// ListingCheckRequest validates e-commerce copy, byte limits, scripts, and policy compliance.
type ListingCheckRequest struct {
	StoreID         string   `json:"storeId,omitempty"`
	Platform        string   `json:"platform"`
	BrandName       string   `json:"brandName,omitempty"`
	Title           string   `json:"title,omitempty"`
	Bullets         []string `json:"bullets,omitempty"`
	Description     string   `json:"description,omitempty"`
	SearchTerms     string   `json:"searchTerms,omitempty"`
	VideoScript     string   `json:"videoScript,omitempty"`
	MetaTitle       string   `json:"metaTitle,omitempty"`
	MetaDescription string   `json:"metaDescription,omitempty"`
	Handle          string   `json:"handle,omitempty"`
	ForbiddenTerms  []string `json:"forbiddenTerms,omitempty"`
}

// ListingIssue represents a single compliance, SEO, or pacing finding.
type ListingIssue struct {
	Severity   string `json:"severity"` // "error" | "warning"
	Code       string `json:"code"`
	Field      string `json:"field"`
	Message    string `json:"message"`
	Matched    string `json:"matched,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ListingCheckResult returns deterministic metrics, compliance findings, and safe trims.
type ListingCheckResult struct {
	Platform               string         `json:"platform"`
	Passed                 bool           `json:"passed"`
	Score                  int            `json:"score"`
	TitleChars             int            `json:"titleChars"`
	TitleBytes             int            `json:"titleBytes"`
	SearchTermsBytes       int            `json:"searchTermsBytes,omitempty"`
	SearchTermsLimitBytes  int            `json:"searchTermsLimitBytes,omitempty"`
	TrimmedSearchTerms     string         `json:"trimmedSearchTerms,omitempty"`
	DuplicateSearchTokens  []string       `json:"duplicateSearchTokens,omitempty"`
	EstimatedSpokenSeconds float64        `json:"estimatedSpokenSeconds,omitempty"`
	HookEstimatedSeconds   float64        `json:"hookEstimatedSeconds,omitempty"`
	Issues                 []ListingIssue `json:"issues"`
}

// ShopifyVariantInput defines one SKU/variant row when generating a Shopify product CSV.
type ShopifyVariantInput struct {
	Option1Value   string  `json:"option1Value,omitempty"`
	Option2Value   string  `json:"option2Value,omitempty"`
	Option3Value   string  `json:"option3Value,omitempty"`
	SKU            string  `json:"sku"`
	Price          float64 `json:"price"`
	CompareAtPrice float64 `json:"compareAtPrice,omitempty"`
	WeightGrams    int     `json:"weightGrams,omitempty"`
	InventoryQty   int     `json:"inventoryQty,omitempty"`
	ImageURL       string  `json:"imageUrl,omitempty"`
}

// ShopifyCSVRequest either builds a Shopify product import CSV or validates an existing one.
type ShopifyCSVRequest struct {
	WorkspaceDir         string                `json:"directory,omitempty"`
	InputCSVPath         string                `json:"inputCsvPath,omitempty"`
	OutputCSVPath        string                `json:"outputCsvPath,omitempty"`
	Handle               string                `json:"handle,omitempty"`
	Title                string                `json:"title,omitempty"`
	BodyHTML             string                `json:"bodyHtml,omitempty"`
	Vendor               string                `json:"vendor,omitempty"`
	ProductCategory      string                `json:"productCategory,omitempty"`
	ProductType          string                `json:"productType,omitempty"`
	Tags                 []string              `json:"tags,omitempty"`
	Status               string                `json:"status,omitempty"`
	Option1Name          string                `json:"option1Name,omitempty"`
	Option2Name          string                `json:"option2Name,omitempty"`
	Option3Name          string                `json:"option3Name,omitempty"`
	Psychological99      bool                  `json:"psychological99,omitempty"`
	CompareAtDiscountPct float64               `json:"compareAtDiscountPct,omitempty"`
	Variants             []ShopifyVariantInput `json:"variants,omitempty"`
}

// ShopifyCSVResult summarizes generated or validated Shopify product CSV rows.
type ShopifyCSVResult struct {
	Valid         bool     `json:"valid"`
	RowCount      int      `json:"rowCount"`
	VariantCount  int      `json:"variantCount"`
	Handle        string   `json:"handle,omitempty"`
	OutputCSVPath string   `json:"outputCsvPath,omitempty"`
	Errors        []string `json:"errors,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

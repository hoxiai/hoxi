package ecom

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const amazonSearchTermsMaxBytes = 249

var (
	htmlTagCheckRe = regexp.MustCompile(`(?is)<\s*/?\s*([a-z0-9]+)\b[^>]*>`)
	handleSlugRe   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	asinTokenRe    = regexp.MustCompile(`(?i)\bB0[A-Z0-9]{8}\b`)
)

var (
	amazonEPATerms = []string{
		"antibacterial", "anti-bacterial", "antimicrobial", "anti-microbial",
		"antifungal", "anti-fungal", "disinfect", "disinfectant", "sterilize",
		"kills germs", "kills bacteria", "kills viruses", "repel insects", "pest control", "mold proof", "mildew proof",
	}
	amazonFDATerms = []string{
		"fda approved", "cures", "cure", "prevent disease", "heals", "clinical trial",
		"treats arthritis", "diabetes", "insomnia relief", "anxiety relief", "medical grade cure",
	}
	amazonPromoTerms = []string{
		"best seller", "bestseller", "#1 rated", "free shipping", "100% money back",
		"limited time sale", "on sale", "buy now", "cheapest",
	}
	tiktokDiversionTerms = []string{
		"link in bio", "whatsapp", "wechat", "telegram", "dm me", "message me on ig",
		"amazon.com", "buy on amazon", "visit our website",
	}
	tiktokExaggeratedTerms = []string{
		"instant weight loss", "burn fat overnight", "100% guaranteed", "miracle cure",
		"lose 10 lbs", "before and after", "permanent results", "no side effects guaranteed",
	}
	cnAdLawSuperlatives = []string{
		"国家级", "最高级", "最佳", "第一", "唯一", "顶级", "极品", "绝对", "销量冠军", "全网最低", "史无前例", "100%有效",
	}
	b2b1688IntentTerms = []string{
		"批发", "代发", "源头", "厂家", "工厂", "定制", "跨境", "现货", "专供", "外贸", "贴牌", "oem", "odm",
	}
)

// CheckListing runs deterministic byte-count, compliance, SEO, and video-script
// pacing checks for Amazon, TikTok Shop, Shopify, and 1688 listings.
func CheckListing(req ListingCheckRequest, defaults *StoreDefaults) (ListingCheckResult, error) {
	if strings.TrimSpace(req.Platform) == "" && strings.TrimSpace(req.SourcePlatform) != "" {
		req.Platform = req.SourcePlatform
	}
	if defaults != nil {
		if strings.TrimSpace(req.Platform) == "" {
			req.Platform = defaults.Platform
		}
		if strings.TrimSpace(req.BrandName) == "" {
			req.BrandName = defaults.BrandName
		}
		if len(req.ForbiddenTerms) == 0 && len(defaults.ForbiddenTerms) > 0 {
			req.ForbiddenTerms = defaults.ForbiddenTerms
		}
	}
	platform := NormalizePlatform(req.Platform)
	switch platform {
	case "amazon", "tiktok-shop", "shopify", "1688":
	default:
		return ListingCheckResult{}, fmt.Errorf("unsupported platform %q (expected amazon, tiktok-shop, shopify, or 1688)", req.Platform)
	}

	title := strings.TrimSpace(req.Title)
	res := ListingCheckResult{
		Platform:   platform,
		TitleChars: utf8.RuneCountInString(title),
		TitleBytes: len(title),
		Issues:     []ListingIssue{},
	}

	addIssue := func(sev, code, field, msg, matched, suggestion string) {
		res.Issues = append(res.Issues, ListingIssue{
			Severity:   sev,
			Code:       code,
			Field:      field,
			Message:    msg,
			Matched:    matched,
			Suggestion: suggestion,
		})
	}

	fullCorpus := strings.Join(append([]string{title, req.Description, req.SearchTerms, req.VideoScript, req.MetaTitle, req.MetaDescription}, req.Bullets...), "\n")
	lowerCorpus := strings.ToLower(fullCorpus)

	switch platform {
	case "amazon":
		if title == "" {
			addIssue("error", "AMZ_EMPTY_TITLE", "title", "Amazon title is required.", "", "Include Brand + Core Product + Key Material/Spec + Size/Count.")
		} else {
			if res.TitleChars > 200 {
				addIssue("error", "AMZ_TITLE_TOO_LONG", "title", fmt.Sprintf("Title is %d chars (Amazon hard cap is 200 chars; <=160 recommended for mobile).", res.TitleChars), "", "Trim redundant adjectives to <= 180 chars.")
			} else if res.TitleChars < 60 {
				addIssue("warning", "AMZ_TITLE_UNDERUTILIZED", "title", fmt.Sprintf("Title is only %d chars; keyword indexing space is underutilized.", res.TitleChars), "", "Expand to 120–180 chars with primary search attributes.")
			}
			if strings.ContainsAny(title, "!$?_{}^¬¦") {
				addIssue("error", "AMZ_TITLE_FORBIDDEN_SYMBOL", "title", "Title contains prohibited decorative/promotional symbols (!, $, ?, _, {, }, ^).", "", "Use hyphens (-), pipes (|), or commas instead.")
			}
			if req.BrandName != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(strings.TrimSpace(req.BrandName))) {
				addIssue("warning", "AMZ_BRAND_MISSING_IN_TITLE", "title", fmt.Sprintf("Store brand %q is missing from the title.", req.BrandName), req.BrandName, "Start the Amazon title with the exact registered brand name.")
			}
		}

		if len(req.Bullets) > 0 {
			if len(req.Bullets) != 5 {
				addIssue("warning", "AMZ_BULLET_COUNT", "bullets", fmt.Sprintf("Provided %d bullet points (standard Amazon listing uses 5 bullets).", len(req.Bullets)), "", "Provide 5 benefit-driven bullet points.")
			}
			for i, b := range req.Bullets {
				bChars := utf8.RuneCountInString(strings.TrimSpace(b))
				field := fmt.Sprintf("bullets[%d]", i)
				if bChars > 500 {
					addIssue("error", "AMZ_BULLET_TOO_LONG", field, fmt.Sprintf("Bullet %d is %d chars (>500 chars risks suppression/truncation).", i+1, bChars), "", "Keep each bullet between 150 and 240 chars.")
				} else if bChars > 260 {
					addIssue("warning", "AMZ_BULLET_VERBOSE", field, fmt.Sprintf("Bullet %d is %d chars (>250 chars reduces mobile readability).", i+1, bChars), "", "Keep each bullet concise (<=240 chars).")
				}
				if htmlTagCheckRe.MatchString(b) {
					addIssue("error", "AMZ_HTML_IN_BULLET", field, "HTML tags are prohibited in Amazon bullet points.", "", "Use plain text and capitalized lead-in phrases.")
				}
			}
		}

		if strings.TrimSpace(req.SearchTerms) != "" {
			res.SearchTermsLimitBytes = amazonSearchTermsMaxBytes
			st := strings.Join(strings.Fields(req.SearchTerms), " ")
			res.SearchTermsBytes = len(st)
			trimmed, dups := optimizeAmazonSearchTerms(st, req.BrandName, amazonSearchTermsMaxBytes)
			res.TrimmedSearchTerms = trimmed
			res.DuplicateSearchTokens = dups
			if res.SearchTermsBytes > amazonSearchTermsMaxBytes {
				addIssue("error", "AMZ_SEARCH_TERMS_BYTE_OVERFLOW", "searchTerms",
					fmt.Sprintf("Backend Search Terms is %d UTF-8 bytes (exceeds Amazon 249-byte limit; Amazon drops the ENTIRE field when >=250 bytes!).", res.SearchTermsBytes),
					"", "Use trimmedSearchTerms (deduplicated & byte-safe <= 249 UTF-8 bytes).")
			}
			if len(dups) > 0 {
				addIssue("warning", "AMZ_SEARCH_TERMS_DUPLICATES", "searchTerms",
					fmt.Sprintf("Backend Search Terms repeats %d token(s), wasting byte budget.", len(dups)),
					strings.Join(dups, ", "), "Remove repeated words and brand/ASIN tokens.")
			}
			if m := asinTokenRe.FindString(st); m != "" {
				addIssue("error", "AMZ_COMPETITOR_ASIN_IN_SEARCH_TERMS", "searchTerms", "Competitor ASINs in backend Search Terms violate Amazon policy.", m, "Remove ASIN identifiers from Search Terms.")
			}
		}

		checkPhraseList(lowerCorpus, amazonEPATerms, func(hit string) {
			addIssue("error", "AMZ_EPA_PESTICIDE_TRIGGER", "corpus", "Pesticide/antimicrobial claim triggers Amazon EPA FIFRA listing takedown unless EPA-registered.", hit, "Remove antimicrobial/antibacterial/disinfecting claims.")
		})
		checkPhraseList(lowerCorpus, amazonFDATerms, func(hit string) {
			addIssue("error", "AMZ_FDA_MEDICAL_CLAIM", "corpus", "Unapproved medical/disease claim risks FDA compliance suspension.", hit, "Describe cosmetic/comfort support without disease or cure claims.")
		})
		checkPhraseList(lowerCorpus, amazonPromoTerms, func(hit string) {
			addIssue("warning", "AMZ_PROMO_CLAIM", "corpus", "Subjective ranking or time-sensitive promotional phrase violates Amazon style guide.", hit, "Replace with verifiable product specs.")
		})

	case "tiktok-shop":
		if title != "" {
			if res.TitleChars > 255 {
				addIssue("error", "TTS_TITLE_TOO_LONG", "title", fmt.Sprintf("TikTok Shop title is %d chars (max 255).", res.TitleChars), "", "Keep title between 40 and 150 chars.")
			} else if res.TitleChars < 35 {
				addIssue("warning", "TTS_TITLE_TOO_SHORT", "title", fmt.Sprintf("TikTok Shop title is %d chars (<35 chars hurts search discovery).", res.TitleChars), "", "Expand title to 50–120 chars with problem/use-case keywords.")
			}
		}
		if script := strings.TrimSpace(req.VideoScript); script != "" {
			totalSec, hookSec := estimateScriptTiming(script)
			res.EstimatedSpokenSeconds = round2(totalSec)
			res.HookEstimatedSeconds = round2(hookSec)
			if hookSec > 4.0 {
				addIssue("warning", "TTS_SLOW_OPENING_HOOK", "videoScript",
					fmt.Sprintf("Opening hook sentence takes ~%.1fs to speak (>3.5s risks swipe-away).", hookSec),
					"", "Punch up the first sentence to <= 8 English words or <= 14 Chinese chars.")
			}
			if totalSec < 12 || totalSec > 65 {
				addIssue("warning", "TTS_SCRIPT_DURATION", "videoScript",
					fmt.Sprintf("Estimated spoken duration is %.1fs (high-converting TikTok Shop product clips typically run 15–45s).", totalSec),
					"", "Aim for 18–40s: 0–3s Hook -> 3–15s Pain/Demo -> 15–25s Proof -> 25–30s Yellow-Cart CTA.")
			}
		}
		checkPhraseList(lowerCorpus, tiktokDiversionTerms, func(hit string) {
			addIssue("error", "TTS_OFF_PLATFORM_DIVERSION", "corpus", "Off-platform traffic diversion phrase triggers TikTok Shop violation points.", hit, "Direct viewers strictly to the TikTok Shop orange/yellow cart.")
		})
		checkPhraseList(lowerCorpus, tiktokExaggeratedTerms, func(hit string) {
			addIssue("error", "TTS_EXAGGERATED_CLAIM", "corpus", "Absolute/miracle/before-and-after claim risks TikTok Shop video/product rejection.", hit, "Use realistic, demonstrable product benefits.")
		})

	case "shopify":
		metaTitle := strings.TrimSpace(req.MetaTitle)
		if metaTitle == "" {
			metaTitle = title
		}
		if mtLen := utf8.RuneCountInString(metaTitle); mtLen > 65 {
			addIssue("warning", "SHOPIFY_META_TITLE_LONG", "metaTitle", fmt.Sprintf("SEO title is %d chars (>60–65 chars truncates in Google SERPs).", mtLen), "", "Keep SEO title between 35 and 60 chars.")
		}
		if md := strings.TrimSpace(req.MetaDescription); md != "" {
			if mdLen := utf8.RuneCountInString(md); mdLen > 160 {
				addIssue("warning", "SHOPIFY_META_DESC_LONG", "metaDescription", fmt.Sprintf("Meta description is %d chars (>160 chars truncates in Google SERPs).", mdLen), "", "Keep meta description between 110 and 155 chars.")
			}
		} else {
			addIssue("warning", "SHOPIFY_META_DESC_MISSING", "metaDescription", "Meta description is empty.", "", "Add a 120–155 char conversion-focused meta description.")
		}
		if h := strings.TrimSpace(req.Handle); h != "" && !handleSlugRe.MatchString(h) {
			addIssue("error", "SHOPIFY_INVALID_HANDLE", "handle", fmt.Sprintf("URL handle %q must be lowercase alphanumeric hyphen-separated.", h), h, "Format handle like 'ergonomic-office-chair'.")
		}

	case "1688":
		audit1688Listing(req, title, lowerCorpus, addIssue)
	}

	// Custom store-level forbidden terms (e.g. competitor trademarks or brand blacklist).
	for _, rawTerm := range req.ForbiddenTerms {
		term := strings.TrimSpace(rawTerm)
		if term == "" {
			continue
		}
		if containsPhraseBoundary(lowerCorpus, term) {
			addIssue("error", "STORE_FORBIDDEN_TERM", "corpus", fmt.Sprintf("Listing contains store-blocked term %q.", term), term, "Remove the blocked keyword/trademark.")
		}
	}

	score := 100
	errCount := 0
	for _, iss := range res.Issues {
		if iss.Severity == "error" {
			errCount++
			score -= 20
		} else {
			score -= 6
		}
	}
	if score < 0 {
		score = 0
	}
	res.Score = score
	res.Passed = errCount == 0
	return res, nil
}

func isASCIIWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func containsPhraseBoundary(lowerCorpus, rawPhrase string) bool {
	p := strings.ToLower(strings.TrimSpace(rawPhrase))
	if p == "" {
		return false
	}
	checkLeft := isASCIIWordByte(p[0])
	checkRight := isASCIIWordByte(p[len(p)-1])
	for offset := 0; offset < len(lowerCorpus); {
		idx := strings.Index(lowerCorpus[offset:], p)
		if idx < 0 {
			return false
		}
		pos := offset + idx
		end := pos + len(p)
		leftOK := !checkLeft || pos == 0 || !isASCIIWordByte(lowerCorpus[pos-1])
		rightOK := !checkRight || end == len(lowerCorpus) || !isASCIIWordByte(lowerCorpus[end])
		if leftOK && rightOK {
			return true
		}
		offset = pos + 1
	}
	return false
}

func checkPhraseList(lowerCorpus string, phrases []string, onMatch func(hit string)) {
	for _, p := range phrases {
		if containsPhraseBoundary(lowerCorpus, p) {
			onMatch(strings.TrimSpace(p))
		}
	}
}

func optimizeAmazonSearchTerms(raw, brand string, maxBytes int) (string, []string) {
	seen := make(map[string]bool)
	brandTokens := make(map[string]bool)
	for _, bt := range strings.Fields(strings.ToLower(brand)) {
		if len(bt) >= 2 {
			brandTokens[bt] = true
		}
	}
	var unique []string
	var dups []string
	dupSeen := make(map[string]bool)
	for _, tok := range strings.Fields(raw) {
		cleaned := strings.Trim(tok, ",.;:|/\\")
		if cleaned == "" || asinTokenRe.MatchString(cleaned) {
			continue
		}
		key := strings.ToLower(cleaned)
		if brandTokens[key] {
			continue
		}
		if seen[key] {
			if !dupSeen[key] {
				dupSeen[key] = true
				dups = append(dups, key)
			}
			continue
		}
		seen[key] = true
		unique = append(unique, cleaned)
	}
	var out strings.Builder
	for _, tok := range unique {
		sep := ""
		if out.Len() > 0 {
			sep = " "
		}
		if out.Len()+len(sep)+len(tok) > maxBytes {
			if out.Len() == 0 {
				// Single CJK/continuous string without spaces: trim strictly on UTF-8 rune boundary.
				for _, r := range tok {
					rl := utf8.RuneLen(r)
					if rl < 0 || out.Len()+rl > maxBytes {
						break
					}
					out.WriteRune(r)
				}
			}
			break
		}
		out.WriteString(sep)
		out.WriteString(tok)
	}
	return out.String(), dups
}

func estimateScriptTiming(script string) (totalSec, hookSec float64) {
	spoken := func(s string) float64 {
		cjk := 0
		var asciiBuf strings.Builder
		for _, r := range s {
			if unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
				cjk++
				asciiBuf.WriteByte(' ')
			} else {
				asciiBuf.WriteRune(r)
			}
		}
		enWords := len(strings.Fields(asciiBuf.String()))
		return float64(enWords)/2.5 + float64(cjk)/4.2
	}
	totalSec = spoken(script)
	firstHook := script
	for i, r := range script {
		if r == '.' && i > 0 && i+1 < len(script) && script[i-1] >= '0' && script[i-1] <= '9' && script[i+1] >= '0' && script[i+1] <= '9' {
			continue
		}
		if r == '.' || r == '!' || r == '?' || r == '。' || r == '！' || r == '？' || r == '\n' {
			if strings.TrimSpace(script[:i]) != "" {
				firstHook = script[:i]
				break
			}
		}
	}
	hookSec = spoken(firstHook)
	return totalSec, hookSec
}

func cjkWeightedUnits(s string) int {
	units := 0
	for _, r := range s {
		if r > 127 {
			units += 2
		} else {
			units++
		}
	}
	return units
}

func audit1688Listing(req ListingCheckRequest, title, lowerCorpus string, addIssue func(sev, code, field, msg, matched, suggestion string)) {
	if title == "" {
		addIssue("error", "ALIBABA_1688_EMPTY_TITLE", "title", "1688 product title is required.", "", "Compose a 24–30 Chinese char title: 源头厂家/跨境专供 + 材质规格 + 核心品类词 + 一件代发/批发.")
	} else {
		unitWeight := cjkWeightedUnits(title)
		if unitWeight > 60 {
			addIssue("error", "ALIBABA_1688_TITLE_OVERFLOW", "title", fmt.Sprintf("1688 title exceeds 60-byte/30-Chinese-char ceiling (weighted length=%d).", unitWeight), "", "Trim to <= 30 Chinese chars (60 weighted units).")
		} else if unitWeight < 36 {
			addIssue("warning", "ALIBABA_1688_TITLE_SHORT", "title", fmt.Sprintf("1688 title uses %d/60 weighted units; B2B wholesale search slots are underutilized.", unitWeight), "", "Include 源头厂家/一件代发/跨境专供 + core material + product word.")
		}
		lowerTitle := strings.ToLower(title)
		hasB2BIntent := false
		for _, kw := range b2b1688IntentTerms {
			if containsPhraseBoundary(lowerTitle, kw) {
				hasB2BIntent = true
				break
			}
		}
		if !hasB2BIntent {
			addIssue("warning", "ALIBABA_1688_MISSING_B2B_INTENT", "title", "1688 title lacks B2B wholesale or cross-border supply intent keywords.", "", "Add B2B buyer intent words such as 源头厂家, 一件代发, 跨境专供, 定制, or 批发.")
		}
		if stuffed := repeated1688TitlePhrase(title); stuffed != "" {
			addIssue("warning", "ALIBABA_1688_KEYWORD_STUFFING", "title", fmt.Sprintf("1688 title repeats %q >=3 times, risking search demotion for keyword stuffing.", stuffed), stuffed, "Replace duplicate words with distinct material, scenario, or specification modifiers.")
		}
	}

	checkPhraseList(lowerCorpus, cnAdLawSuperlatives, func(hit string) {
		addIssue("error", "CN_AD_LAW_SUPERLATIVE", "corpus", "China Advertising Law (广告法) prohibits absolute superlative claims.", hit, "Replace with objective specifications or verified test metrics.")
	})

	imgCount := req.ImageCount
	if imgCount == 0 && len(req.Images) > 0 {
		imgCount = len(req.Images)
	}
	if imgCount > 0 || req.CheckSellerQuality {
		if imgCount < 5 {
			addIssue("warning", "ALIBABA_1688_IMAGE_COUNT_LOW", "images", fmt.Sprintf("1688 main gallery has %d image(s); 7-star quality requires >= 5 800x800+ images.", imgCount), "", "Supply >= 5 main images (selling-point hero, scenario, detail, spec/dimension card, and pure-white-bg slot #5 via qingpu-mcp generate_ecommerce_image).")
		}
	}
	if req.HasWhiteBgImage != nil {
		if !*req.HasWhiteBgImage {
			addIssue("warning", "ALIBABA_1688_WHITE_BG_MISSING", "hasWhiteBgImage", "Missing clean pure-white background product image (1688 5th gallery slot / cross-border channel pool requirement).", "", "Run qingpu-mcp edit_product_image(mode=\"image-bg-remove\") or generate_ecommerce_image(imageType=\"white-bg\") to produce a clean white-background asset.")
		}
	} else if req.CheckSellerQuality {
		addIssue("warning", "ALIBABA_1688_WHITE_BG_UNVERIFIED", "hasWhiteBgImage", "5th pure-white background image status was not verified.", "", "Verify slot #5 is a pure-white logo-free product image (--white-bg true/false).")
	}
	if req.HasVideo != nil && !*req.HasVideo {
		addIssue("warning", "ALIBABA_1688_VIDEO_MISSING", "hasVideo", "Main gallery video is missing, lowering 1688 7-star conversion weight.", "", "Upload a 9–30s 1:1 or 3:4 factory/product demonstration video.")
	}

	attrCount := req.AttributeCount
	if attrCount == 0 && len(req.Attributes) > 0 {
		attrCount = countJSONAttributes(req.Attributes)
	}
	if (attrCount > 0 && attrCount < 8) || (req.CheckSellerQuality && attrCount < 8) {
		addIssue("warning", "ALIBABA_1688_ATTRIBUTES_SPARSE", "attributeCount", fmt.Sprintf("Only %d structured product attribute(s) filled; 1688 filter traffic & Ozon/WB mapping expect >= 8.", attrCount), "", "Complete material, dimensions, net weight, packing quantity, origin, model, and customization attributes.")
	}

	if len(req.PriceBrackets) > 0 {
		for i, b := range req.PriceBrackets {
			if b.MinQty <= 0 || b.UnitCostCNY <= 0 {
				addIssue("error", "ALIBABA_1688_INVALID_PRICE_TIERS", "priceBrackets", fmt.Sprintf("Wholesale bracket #%d has non-positive minQty (%d) or unitCostCNY (%.2f).", i+1, b.MinQty, b.UnitCostCNY), "", "Ensure every MOQ tier has minQty > 0 and unitCostCNY > 0.")
				break
			}
			if i > 0 {
				prev := req.PriceBrackets[i-1]
				if b.MinQty <= prev.MinQty || b.UnitCostCNY > prev.UnitCostCNY {
					addIssue("error", "ALIBABA_1688_INVALID_PRICE_TIERS", "priceBrackets", fmt.Sprintf("Wholesale brackets must have strictly increasing minQty and non-increasing unit price (got %d@¥%.2f -> %d@¥%.2f).", prev.MinQty, prev.UnitCostCNY, b.MinQty, b.UnitCostCNY), "", "Order brackets ascending by MOQ with progressively lower unit prices (e.g. 2:19.8,50:17.5,200:15.8).")
					break
				}
			}
		}
		if len(req.PriceBrackets) < 2 {
			addIssue("warning", "ALIBABA_1688_SINGLE_PRICE_TIER", "priceBrackets", "Only 1 wholesale price tier configured; multi-tier MOQ pricing improves B2B inquiry & dropship conversion.", "", "Configure 2–3 MOQ tiers (dropship/sample -> small wholesale -> carton volume).")
		}
	} else if req.CheckSellerQuality {
		addIssue("warning", "ALIBABA_1688_SINGLE_PRICE_TIER", "priceBrackets", "Wholesale MOQ price brackets are missing.", "", "Provide --brackets minQty:cny,... (2–3 MOQ tiers recommended).")
	}

	hasAnyPack := req.WeightKg > 0 || req.LengthCm > 0 || req.WidthCm > 0 || req.HeightCm > 0
	hasFullPack := req.WeightKg > 0 && req.LengthCm > 0 && req.WidthCm > 0 && req.HeightCm > 0
	if (hasAnyPack && !hasFullPack) || (req.CheckSellerQuality && !hasFullPack) {
		addIssue("warning", "ALIBABA_1688_PACKAGING_INCOMPLETE", "packaging", "Cross-border unit weight (weightKg) or package dimensions (LxWxH cm) are incomplete.", "", "Fill accurate unit weight and LxWxH cm for 1688 cross-border pool & Ozon/WB/Amazon volumetric freight.")
	}
}

func repeated1688TitlePhrase(title string) string {
	var (
		hanRunes      []rune
		firstRepeated string
		counts        = make(map[string]int)
	)
	flushHan := func() {
		if firstRepeated != "" {
			hanRunes = hanRunes[:0]
			return
		}
		// Non-overlapping greedy scan per bigram avoids counting "AAA" as 2x "AA".
		seenPos := make(map[string]int)
		for i := 0; i+1 < len(hanRunes); i++ {
			bg := string(hanRunes[i : i+2])
			if prev, ok := seenPos[bg]; ok && i < prev+2 {
				continue
			}
			seenPos[bg] = i
			counts[bg]++
			if counts[bg] >= 3 {
				firstRepeated = bg
				break
			}
		}
		hanRunes = hanRunes[:0]
	}
	for _, r := range title {
		if unicode.Is(unicode.Han, r) {
			hanRunes = append(hanRunes, r)
		} else {
			flushHan()
		}
	}
	flushHan()
	if firstRepeated != "" {
		return firstRepeated
	}
	wordCounts := make(map[string]int)
	for _, tok := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(tok) >= 3 {
			wordCounts[tok]++
			if wordCounts[tok] >= 3 {
				return tok
			}
		}
	}
	return ""
}

func countJSONAttributes(raw json.RawMessage) int {
	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return len(arr)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return len(obj)
	}
	return 0
}

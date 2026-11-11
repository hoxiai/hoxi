package ecom

import (
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
)

// CheckListing runs deterministic byte-count, compliance, SEO, and video-script
// pacing checks for Amazon, TikTok Shop, Shopify, and 1688 listings.
func CheckListing(req ListingCheckRequest, defaults *StoreDefaults) (ListingCheckResult, error) {
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
		if title != "" {
			unitWeight := cjkWeightedUnits(title)
			if unitWeight > 60 {
				addIssue("error", "ALIBABA_1688_TITLE_OVERFLOW", "title", fmt.Sprintf("1688 title exceeds 60-byte/30-Chinese-char ceiling (weighted length=%d).", unitWeight), "", "Trim to <= 30 Chinese chars (60 weighted units).")
			} else if unitWeight < 36 {
				addIssue("warning", "ALIBABA_1688_TITLE_SHORT", "title", fmt.Sprintf("1688 title uses %d/60 weighted units; B2B wholesale search slots are underutilized.", unitWeight), "", "Include 源头厂家/一件代发/跨境专供 + core material + product word.")
			}
		}
		checkPhraseList(lowerCorpus, cnAdLawSuperlatives, func(hit string) {
			addIssue("error", "CN_AD_LAW_SUPERLATIVE", "corpus", "China Advertising Law (广告法) prohibits absolute superlative claims.", hit, "Replace with objective specifications or verified test metrics.")
		})
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

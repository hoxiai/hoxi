package ecom

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// AuditAdsReport parses an Amazon / TikTok / Google / Shopify Search Term or
// Campaign report (.xlsx, .xls, .ods, .csv, .tsv), classifies wasteful bleeders,
// high-ACOS bid-downs, winning harvest terms, and low-CTR creatives, and
// optionally exports an actionable multi-sheet .xlsx workbook.
func AuditAdsReport(req AdsAuditRequest, defaults *StoreDefaults, sheetIO SpreadsheetIO) (AdsAuditResult, error) {
	if sheetIO == nil {
		return AdsAuditResult{}, errors.New("spreadsheet I/O provider is not configured")
	}
	if defaults != nil && req.TargetACOSPct <= 0 {
		if defaults.TargetACOSPct > 0 {
			req.TargetACOSPct = defaults.TargetACOSPct
		} else if defaults.TargetROAS > 0 {
			req.TargetACOSPct = 100.0 / defaults.TargetROAS
		}
	}
	targetACOS := req.TargetACOSPct
	if targetACOS <= 0 {
		targetACOS = 25.0
	}
	wasteSpend := req.WasteSpendThreshold
	if wasteSpend <= 0 {
		wasteSpend = 15.0
	}
	wasteClicks := req.WasteClickThreshold
	if wasteClicks <= 0 {
		wasteClicks = 12
	}
	winnerOrders := req.WinnerMinOrders
	if winnerOrders <= 0 {
		winnerOrders = 2
	}
	maxItems := req.MaxItemsPerBucket
	if maxItems <= 0 || maxItems > 500 {
		maxItems = 100
	}

	reportPath, err := resolveSafeEcomPath(req.WorkspaceDir, req.ReportPath)
	if err != nil {
		return AdsAuditResult{}, fmt.Errorf("invalid reportPath: %w", err)
	}
	var outXLSX string
	if strings.TrimSpace(req.OutputXLSXPath) != "" {
		outXLSX, err = resolveSafeEcomPath(req.WorkspaceDir, req.OutputXLSXPath)
		if err != nil {
			return AdsAuditResult{}, fmt.Errorf("invalid outputXlsxPath: %w", err)
		}
		if !strings.EqualFold(filepath.Ext(outXLSX), ".xlsx") {
			return AdsAuditResult{}, errors.New("outputXlsxPath must end with .xlsx")
		}
	}

	sheets, err := sheetIO.ParseSpreadsheet(reportPath, 50000)
	if err != nil {
		return AdsAuditResult{}, fmt.Errorf("parse report spreadsheet: %w", err)
	}
	if len(sheets) == 0 {
		return AdsAuditResult{}, errors.New("report contains no sheets")
	}

	colMap, dataRows, err := locateAdReportTable(sheets)
	if err != nil {
		return AdsAuditResult{}, err
	}

	type aggKey struct {
		campaign string
		adGroup  string
		term     string
		match    string
	}
	grouped := make(map[aggKey]*AdsAuditRow)
	var orderKeys []aggKey
	rowsParsed := 0

	for _, row := range dataRows {
		term := cellAt(row, colMap.term)
		if term == "" && colMap.targeting >= 0 {
			term = cellAt(row, colMap.targeting)
		}
		if term == "" || isSummaryFooterRow(term) {
			continue
		}
		imp := int(math.Round(parseMetricNumber(cellAt(row, colMap.impressions))))
		clicks := int(math.Round(parseMetricNumber(cellAt(row, colMap.clicks))))
		spend := parseMetricNumber(cellAt(row, colMap.spend))
		sales := parseMetricNumber(cellAt(row, colMap.sales))
		orders := int(math.Round(parseMetricNumber(cellAt(row, colMap.orders))))
		if imp == 0 && clicks == 0 && spend == 0 && sales == 0 && orders == 0 {
			continue
		}
		rowsParsed++
		k := aggKey{
			campaign: cellAt(row, colMap.campaign),
			adGroup:  cellAt(row, colMap.adGroup),
			term:     term,
			match:    cellAt(row, colMap.matchType),
		}
		existing := grouped[k]
		if existing == nil {
			existing = &AdsAuditRow{
				SearchTerm: term,
				Campaign:   k.campaign,
				AdGroup:    k.adGroup,
				MatchType:  k.match,
			}
			grouped[k] = existing
			orderKeys = append(orderKeys, k)
		}
		existing.Impressions += imp
		existing.Clicks += clicks
		existing.Spend += spend
		existing.Sales += sales
		existing.Orders += orders
	}
	if rowsParsed == 0 {
		return AdsAuditResult{}, errors.New("no valid metric rows found in report")
	}

	var summary AdsAuditSummary
	summary.RowsParsed = rowsParsed
	summary.UniqueTerms = len(grouped)
	summary.TargetACOSPct = round2(targetACOS)

	bleeders := []AdsAuditRow{}
	highACOS := []AdsAuditRow{}
	winners := []AdsAuditRow{}
	lowCTR := []AdsAuditRow{}

	for _, k := range orderKeys {
		r := *grouped[k]
		r.Spend = round2(r.Spend)
		r.Sales = round2(r.Sales)
		if r.Impressions > 0 {
			r.CTRPct = round2((float64(r.Clicks) / float64(r.Impressions)) * 100.0)
		}
		if r.Clicks > 0 {
			r.CVRPct = round2((float64(r.Orders) / float64(r.Clicks)) * 100.0)
			r.CPC = round2(r.Spend / float64(r.Clicks))
		}
		if r.Sales > 0 {
			r.ACOSPct = round2((r.Spend / r.Sales) * 100.0)
		}
		if r.Spend > 0 {
			r.ROAS = round2(r.Sales / r.Spend)
		}

		summary.TotalImpressions += r.Impressions
		summary.TotalClicks += r.Clicks
		summary.TotalSpend += r.Spend
		summary.TotalSales += r.Sales
		summary.TotalOrders += r.Orders
		if r.Orders == 0 {
			summary.WastedSpend += r.Spend
		}

		switch {
		case r.Orders == 0 && (r.Spend >= wasteSpend || r.Clicks >= wasteClicks):
			if r.Spend >= wasteSpend*2 && len(strings.Fields(r.SearchTerm)) <= 2 {
				r.Action = "Add Negative Phrase"
			} else {
				r.Action = "Add Negative Exact"
			}
			bleeders = append(bleeders, r)

		case r.Orders > 0 && r.Sales > 0 && r.ACOSPct > targetACOS*1.15:
			if r.CPC > 0 {
				r.SuggestedBid = math.Max(0.05, round2(r.CPC*(targetACOS/r.ACOSPct)))
			}
			r.Action = fmt.Sprintf("Lower bid to %.2f (target ACOS %.1f%%)", r.SuggestedBid, targetACOS)
			highACOS = append(highACOS, r)

		case r.Orders >= winnerOrders && (r.Sales == 0 || r.ACOSPct <= targetACOS):
			if r.CPC > 0 {
				r.SuggestedBid = round2(r.CPC * 1.12)
			}
			upperMatch := strings.ToUpper(r.MatchType)
			if upperMatch == "EXACT" {
				r.Action = "Scale Exact bid +10%~15% & protect top-of-search"
			} else {
				r.Action = "Harvest to Exact campaign & negate in Auto/Broad"
			}
			winners = append(winners, r)
		}

		if r.Impressions >= 1000 && r.CTRPct < 0.25 {
			c := r
			c.Action = "Audit main image, price badge & title relevance (CTR < 0.25%)"
			lowCTR = append(lowCTR, c)
		}
	}

	sort.Slice(bleeders, func(i, j int) bool { return bleeders[i].Spend > bleeders[j].Spend })
	sort.Slice(highACOS, func(i, j int) bool { return highACOS[i].Spend > highACOS[j].Spend })
	sort.Slice(winners, func(i, j int) bool { return winners[i].Sales > winners[j].Sales })
	sort.Slice(lowCTR, func(i, j int) bool { return lowCTR[i].Impressions > lowCTR[j].Impressions })

	summary.TotalSpend = round2(summary.TotalSpend)
	summary.TotalSales = round2(summary.TotalSales)
	summary.WastedSpend = round2(summary.WastedSpend)
	if summary.TotalImpressions > 0 {
		summary.OverallCTRPct = round2((float64(summary.TotalClicks) / float64(summary.TotalImpressions)) * 100.0)
	}
	if summary.TotalClicks > 0 {
		summary.OverallCVRPct = round2((float64(summary.TotalOrders) / float64(summary.TotalClicks)) * 100.0)
		summary.OverallCPC = round2(summary.TotalSpend / float64(summary.TotalClicks))
	}
	if summary.TotalSales > 0 {
		summary.OverallACOSPct = round2((summary.TotalSpend / summary.TotalSales) * 100.0)
	}
	if summary.TotalSpend > 0 {
		summary.OverallROAS = round2(summary.TotalSales / summary.TotalSpend)
		summary.WastedSpendSharePct = round2((summary.WastedSpend / summary.TotalSpend) * 100.0)
	}

	res := AdsAuditResult{
		Summary:       summary,
		Bleeders:      clipAuditRows(bleeders, maxItems),
		HighACOS:      clipAuditRows(highACOS, maxItems),
		Winners:       clipAuditRows(winners, maxItems),
		LowCTRHighImp: clipAuditRows(lowCTR, maxItems),
	}
	if outXLSX != "" {
		if err := writeAdsAuditWorkbook(outXLSX, res, sheetIO); err != nil {
			return AdsAuditResult{}, fmt.Errorf("write output workbook: %w", err)
		}
		res.OutputXLSXPath = outXLSX
	}
	return res, nil
}

type adColumnIndex struct {
	campaign    int
	adGroup     int
	matchType   int
	targeting   int
	term        int
	impressions int
	clicks      int
	spend       int
	sales       int
	orders      int
}

func locateAdReportTable(sheets []SpreadsheetSheet) (adColumnIndex, [][]string, error) {
	for _, sheet := range sheets {
		limit := len(sheet.Rows)
		if limit > 15 {
			limit = 15
		}
		for rIdx := 0; rIdx < limit; rIdx++ {
			idx := matchAdHeaders(sheet.Rows[rIdx])
			if (idx.term >= 0 || idx.targeting >= 0) && idx.spend >= 0 && (idx.clicks >= 0 || idx.sales >= 0) {
				return idx, sheet.Rows[rIdx+1:], nil
			}
		}
	}
	return adColumnIndex{}, nil, errors.New("could not detect Search Term / Spend / Clicks / Sales columns in report")
}

func matchAdHeaders(headerRow []string) adColumnIndex {
	idx := adColumnIndex{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1}
	for i, raw := range headerRow {
		h := normalizeHeaderKey(raw)
		switch {
		case idx.term < 0 && hasAnyHeader(h, "customersearchterm", "searchterm", "searchquery", "query", "客户搜索词", "买家搜索词", "搜索词"):
			idx.term = i
		case idx.targeting < 0 && hasAnyHeader(h, "targeting", "keyword", "投放", "关键词"):
			idx.targeting = i
		case idx.campaign < 0 && hasAnyHeader(h, "campaignname", "campaign", "广告活动名称", "广告活动", "计划名称"):
			idx.campaign = i
		case idx.adGroup < 0 && hasAnyHeader(h, "adgroupname", "adgroup", "广告组名称", "广告组", "单元名称"):
			idx.adGroup = i
		case idx.matchType < 0 && hasAnyHeader(h, "matchtype", "匹配类型"):
			idx.matchType = i
		case idx.impressions < 0 && hasAnyHeader(h, "impressions", "impr", "展示量", "展现量", "曝光量"):
			idx.impressions = i
		case idx.clicks < 0 && hasAnyHeader(h, "clicks", "点击量", "点击次数", "点击"):
			idx.clicks = i
		case idx.spend < 0 && (hasAnyHeader(h, "spend", "cost", "totalspend", "花费", "消耗", "总花费") && !strings.Contains(h, "acos") && !strings.Contains(h, "cpc")):
			idx.spend = i
		case idx.sales < 0 && (strings.Contains(h, "totalsales") || hasAnyHeader(h, "sales", "gmv", "conversionvalue", "revenue", "销售额", "总销售额", "成交金额")):
			idx.sales = i
		case idx.orders < 0 && (strings.Contains(h, "totalorders") || hasAnyHeader(h, "orders", "conversions", "purchases", "订单数", "总订单数", "转化数", "成交笔数")):
			idx.orders = i
		}
	}
	return idx
}

func normalizeHeaderKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\ufeff")))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasAnyHeader(norm string, candidates ...string) bool {
	for _, c := range candidates {
		if norm == c {
			return true
		}
	}
	return false
}

func parseMetricNumber(raw string) float64 {
	s := strings.TrimSpace(raw)
	if s == "" || s == "-" || s == "--" || strings.EqualFold(s, "n/a") {
		return 0
	}
	for _, sym := range []string{"$", "£", "€", "¥", "￥", "USD", "GBP", "EUR", "CNY", ",", "%", " "} {
		s = strings.ReplaceAll(s, sym, "")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func cellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func isSummaryFooterRow(term string) bool {
	lower := strings.ToLower(strings.TrimSpace(term))
	return lower == "total" || lower == "totals" || lower == "grand total" || lower == "总计" || lower == "合计"
}

func clipAuditRows(rows []AdsAuditRow, maxItems int) []AdsAuditRow {
	if len(rows) <= maxItems {
		return rows
	}
	return rows[:maxItems]
}

func sanitizeCellText(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed != "" && strings.ContainsAny(trimmed[:1], "=+-@") {
		return "'" + trimmed
	}
	return trimmed
}

func writeAdsAuditWorkbook(outPath string, res AdsAuditResult, sheetIO SpreadsheetIO) error {
	s := res.Summary
	summaryRows := [][]string{
		{"Metric", "Value", "Formula / Benchmark"},
		{"Rows Parsed", strconv.Itoa(s.RowsParsed), ""},
		{"Unique Search Terms", strconv.Itoa(s.UniqueTerms), ""},
		{"Total Impressions", strconv.Itoa(s.TotalImpressions), ""},
		{"Total Clicks", strconv.Itoa(s.TotalClicks), ""},
		{"Total Spend", fmt.Sprintf("%.2f", s.TotalSpend), ""},
		{"Total Sales", fmt.Sprintf("%.2f", s.TotalSales), ""},
		{"Total Orders", strconv.Itoa(s.TotalOrders), ""},
		{"Overall CTR (%)", "=IFERROR(ROUND(B5/B4*100,2),0)", ">= 0.35% healthy"},
		{"Overall CVR (%)", "=IFERROR(ROUND(B8/B5*100,2),0)", ">= 8.00% healthy"},
		{"Overall CPC", "=IFERROR(ROUND(B6/B5,2),0)", ""},
		{"Overall ACOS (%)", "=IFERROR(ROUND(B6/B7*100,2),0)", fmt.Sprintf("Target <= %.2f%%", s.TargetACOSPct)},
		{"Overall ROAS", "=IFERROR(ROUND(B7/B6,2),0)", ""},
		{"Zero-Order Wasted Spend", fmt.Sprintf("%.2f", s.WastedSpend), ""},
		{"Wasted Spend Share (%)", "=IFERROR(ROUND(B14/B6*100,2),0)", "Aim < 15%"},
	}
	buildSheet := func(name string, rows []AdsAuditRow) SpreadsheetSheet {
		out := [][]string{{"Campaign", "Ad Group", "Match Type", "Search Term", "Impressions", "Clicks", "Spend", "Sales", "Orders", "CPC", "ACOS (%)", "Suggested Bid", "Recommended Action"}}
		for _, r := range rows {
			out = append(out, []string{
				sanitizeCellText(r.Campaign),
				sanitizeCellText(r.AdGroup),
				sanitizeCellText(r.MatchType),
				sanitizeCellText(r.SearchTerm),
				strconv.Itoa(r.Impressions),
				strconv.Itoa(r.Clicks),
				fmt.Sprintf("%.2f", r.Spend),
				fmt.Sprintf("%.2f", r.Sales),
				strconv.Itoa(r.Orders),
				fmt.Sprintf("%.2f", r.CPC),
				fmt.Sprintf("%.2f", r.ACOSPct),
				fmt.Sprintf("%.2f", r.SuggestedBid),
				sanitizeCellText(r.Action),
			})
		}
		return SpreadsheetSheet{Name: name, Rows: out}
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return sheetIO.WriteXLSX(outPath, []SpreadsheetSheet{
		{Name: "Summary", Rows: summaryRows},
		buildSheet("Negative_Keywords", res.Bleeders),
		buildSheet("Bid_Adjustments", res.HighACOS),
		buildSheet("Scale_Winners", res.Winners),
	})
}

func resolveSafeEcomPath(workspaceDir, rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", errors.New("path is empty")
	}
	for _, seg := range strings.FieldsFunc(filepath.ToSlash(trimmed), func(r rune) bool { return r == '/' }) {
		if seg == ".." {
			return "", errors.New("path traversal '..' is not allowed")
		}
	}
	var abs string
	if filepath.IsAbs(trimmed) {
		abs = filepath.Clean(trimmed)
	} else if strings.TrimSpace(workspaceDir) != "" {
		abs = filepath.Join(filepath.Clean(workspaceDir), trimmed)
	} else {
		var err error
		abs, err = filepath.Abs(trimmed)
		if err != nil {
			return "", err
		}
	}
	if hoxipaths.IsDeniedDocOrSpreadsheetPath(abs) {
		return "", errors.New("access to sensitive or protected path is denied")
	}
	if strings.TrimSpace(workspaceDir) != "" {
		wsAbs, err := filepath.Abs(workspaceDir)
		if err != nil {
			return "", err
		}
		if evalWS, evalErr := filepath.EvalSymlinks(wsAbs); evalErr == nil {
			wsAbs = evalWS
		}
		checkTarget := abs
		if evalParent, evalErr := filepath.EvalSymlinks(filepath.Dir(abs)); evalErr == nil {
			checkTarget = filepath.Join(evalParent, filepath.Base(abs))
		}
		rel, err := filepath.Rel(wsAbs, checkTarget)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("path must stay inside the workspace directory")
		}
		if hoxipaths.IsDeniedDocOrSpreadsheetPath(checkTarget) {
			return "", errors.New("access to sensitive or protected path is denied")
		}
	}
	return abs, nil
}

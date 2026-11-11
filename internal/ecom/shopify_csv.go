package ecom

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var shopifyStandardHeaders = []string{
	"Handle", "Title", "Body (HTML)", "Vendor", "Product Category", "Type", "Tags", "Published",
	"Option1 Name", "Option1 Value", "Option2 Name", "Option2 Value", "Option3 Name", "Option3 Value",
	"Variant SKU", "Variant Grams", "Variant Inventory Tracker", "Variant Inventory Qty",
	"Variant Inventory Policy", "Variant Fulfillment Service", "Variant Price", "Variant Compare At Price",
	"Variant Requires Shipping", "Variant Taxable", "Image Src", "Status",
}

// BuildOrValidateShopifyCSV either validates an existing Shopify product import
// CSV or generates a compliant multi-variant Shopify CSV file.
func BuildOrValidateShopifyCSV(req ShopifyCSVRequest, sheetIO SpreadsheetIO) (ShopifyCSVResult, error) {
	if strings.TrimSpace(req.InputCSVPath) != "" {
		return validateExistingShopifyCSV(req, sheetIO)
	}
	return generateShopifyCSV(req, sheetIO)
}

func generateShopifyCSV(req ShopifyCSVRequest, sheetIO SpreadsheetIO) (ShopifyCSVResult, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return ShopifyCSVResult{}, errors.New("title is required when generating Shopify CSV")
	}
	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		handle = slugifyHandle(title)
	}
	if !handleSlugRe.MatchString(handle) {
		return ShopifyCSVResult{}, fmt.Errorf("invalid Shopify handle %q (expected lowercase letters, digits, hyphens)", handle)
	}

	variants := req.Variants
	if len(variants) == 0 {
		return ShopifyCSVResult{}, errors.New("at least one variant is required")
	}

	var outPath string
	if strings.TrimSpace(req.OutputCSVPath) != "" {
		var err error
		outPath, err = resolveSafeEcomPath(req.WorkspaceDir, req.OutputCSVPath)
		if err != nil {
			return ShopifyCSVResult{}, fmt.Errorf("invalid outputCsvPath: %w", err)
		}
		if !strings.EqualFold(filepath.Ext(outPath), ".csv") {
			return ShopifyCSVResult{}, errors.New("outputCsvPath must end with .csv")
		}
	}

	opt1Name := strings.TrimSpace(req.Option1Name)
	if opt1Name == "" {
		if len(variants) == 1 && strings.TrimSpace(variants[0].Option1Value) == "" {
			opt1Name = "Title"
		} else {
			opt1Name = "Option 1"
		}
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status == "" {
		status = "active"
	}
	published := "true"
	if status != "active" {
		published = "false"
	}

	res := ShopifyCSVResult{
		Handle:       handle,
		VariantCount: len(variants),
	}
	skuSeen := make(map[string]bool)
	rows := [][]string{shopifyStandardHeaders}

	for i, v := range variants {
		price := v.Price
		if price <= 0 {
			res.Errors = append(res.Errors, fmt.Sprintf("variant[%d] (%s): price must be > 0", i, v.SKU))
		}
		if req.Psychological99 && price > 1 {
			price = math.Floor(price) + 0.99
		}
		compareAt := v.CompareAtPrice
		if compareAt <= 0 && req.CompareAtDiscountPct > 0 && req.CompareAtDiscountPct < 90 {
			compareAt = price / (1.0 - req.CompareAtDiscountPct/100.0)
			if req.Psychological99 && compareAt > 1 {
				compareAt = math.Floor(compareAt) + 0.99
			}
		}
		if compareAt > 0 && compareAt <= price {
			res.Warnings = append(res.Warnings, fmt.Sprintf("variant[%d] (%s): compareAtPrice (%.2f) should exceed price (%.2f) for strikethrough badge", i, v.SKU, compareAt, price))
		}

		sku := strings.TrimSpace(v.SKU)
		if sku == "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("variant[%d]: SKU is empty", i))
		} else if skuSeen[sku] {
			res.Errors = append(res.Errors, fmt.Sprintf("duplicate SKU %q", sku))
		} else {
			skuSeen[sku] = true
		}

		opt1Val := strings.TrimSpace(v.Option1Value)
		if opt1Val == "" {
			if len(variants) == 1 && opt1Name == "Title" {
				opt1Val = "Default Title"
			} else {
				opt1Val = fmt.Sprintf("Variant %d", i+1)
			}
		}
		compareStr := ""
		if compareAt > price {
			compareStr = fmt.Sprintf("%.2f", round2(compareAt))
		}

		var rowTitle, bodyHTML, vendor, cat, prodType, tags, rPub, rStatus string
		if i == 0 {
			rowTitle = sanitizeCellText(title)
			bodyHTML = sanitizeCellText(req.BodyHTML)
			vendor = sanitizeCellText(req.Vendor)
			cat = sanitizeCellText(req.ProductCategory)
			prodType = sanitizeCellText(req.ProductType)
			tags = sanitizeCellText(strings.Join(req.Tags, ", "))
			rPub = published
			rStatus = status
		}
		rows = append(rows, []string{
			handle,
			rowTitle,
			bodyHTML,
			vendor,
			cat,
			prodType,
			tags,
			rPub,
			sanitizeCellText(opt1Name),
			sanitizeCellText(opt1Val),
			sanitizeCellText(req.Option2Name),
			sanitizeCellText(v.Option2Value),
			sanitizeCellText(req.Option3Name),
			sanitizeCellText(v.Option3Value),
			sanitizeCellText(sku),
			strconv.Itoa(maxInt(0, v.WeightGrams)),
			"shopify",
			strconv.Itoa(maxInt(0, v.InventoryQty)),
			"deny",
			"manual",
			fmt.Sprintf("%.2f", round2(price)),
			compareStr,
			"true",
			"true",
			sanitizeCellText(v.ImageURL),
			rStatus,
		})
	}

	res.RowCount = len(rows) - 1
	res.Valid = len(res.Errors) == 0
	if !res.Valid {
		return res, errors.New(strings.Join(res.Errors, "; "))
	}
	if outPath != "" {
		if sheetIO == nil {
			return res, errors.New("spreadsheet I/O provider is not configured")
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return res, err
		}
		if err := sheetIO.WriteDelimited(outPath, rows, ','); err != nil {
			return res, fmt.Errorf("write Shopify CSV: %w", err)
		}
		res.OutputCSVPath = outPath
	}
	return res, nil
}

func validateExistingShopifyCSV(req ShopifyCSVRequest, sheetIO SpreadsheetIO) (ShopifyCSVResult, error) {
	if sheetIO == nil {
		return ShopifyCSVResult{}, errors.New("spreadsheet I/O provider is not configured")
	}
	inPath, err := resolveSafeEcomPath(req.WorkspaceDir, req.InputCSVPath)
	if err != nil {
		return ShopifyCSVResult{}, fmt.Errorf("invalid inputCsvPath: %w", err)
	}
	sheets, err := sheetIO.ParseSpreadsheet(inPath, 50000)
	if err != nil {
		return ShopifyCSVResult{}, fmt.Errorf("parse CSV: %w", err)
	}
	if len(sheets) == 0 || len(sheets[0].Rows) < 2 {
		return ShopifyCSVResult{}, errors.New("CSV must contain a header row and at least one variant row")
	}
	headers := sheets[0].Rows[0]
	colIdx := make(map[string]int, len(headers))
	for i, h := range headers {
		colIdx[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	var res ShopifyCSVResult
	for _, required := range []string{"handle", "title", "option1 value", "variant price"} {
		if _, ok := colIdx[required]; !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("missing required Shopify column %q", required))
		}
	}
	skuSeen := make(map[string]bool)
	for rIdx, row := range sheets[0].Rows[1:] {
		lineNum := rIdx + 2
		h := cellAt(row, lookupCol(colIdx, "handle"))
		if h == "" {
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: Handle is empty", lineNum))
		} else if !handleSlugRe.MatchString(h) {
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: invalid Handle %q", lineNum, h))
		} else if res.Handle == "" {
			res.Handle = h
		}
		priceStr := cellAt(row, lookupCol(colIdx, "variant price"))
		if priceStr != "" {
			res.VariantCount++
			p, pErr := strconv.ParseFloat(priceStr, 64)
			if pErr != nil || p <= 0 {
				res.Errors = append(res.Errors, fmt.Sprintf("row %d: invalid Variant Price %q", lineNum, priceStr))
			}
			if cmpStr := cellAt(row, lookupCol(colIdx, "variant compare at price")); cmpStr != "" {
				if cmp, cErr := strconv.ParseFloat(cmpStr, 64); cErr != nil || cmp <= p {
					res.Warnings = append(res.Warnings, fmt.Sprintf("row %d: Variant Compare At Price (%s) should exceed Variant Price (%s)", lineNum, cmpStr, priceStr))
				}
			}
		}
		if sku := cellAt(row, lookupCol(colIdx, "variant sku")); sku != "" {
			if skuSeen[sku] {
				res.Errors = append(res.Errors, fmt.Sprintf("row %d: duplicate Variant SKU %q", lineNum, sku))
			}
			skuSeen[sku] = true
		}
	}
	res.RowCount = len(sheets[0].Rows) - 1
	res.Valid = len(res.Errors) == 0
	return res, nil
}

func lookupCol(m map[string]int, key string) int {
	if idx, ok := m[key]; ok {
		return idx
	}
	return -1
}

func slugifyHandle(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen && b.Len() > 0 {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "product-item"
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

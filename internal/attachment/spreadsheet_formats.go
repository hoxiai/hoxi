package attachment

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"strconv"
	"strings"
)

func parseODSFileWithOpts(odsPath string, opts spreadsheetParseOptions) ([]SpreadsheetSheet, error) {
	zr, err := zip.OpenReader(odsPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "/") != "content.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()

		dec := xml.NewDecoder(io.LimitReader(rc, 16*1024*1024))
		var sheets []SpreadsheetSheet
		var curSheet *SpreadsheetSheet
		var curRowNums []int
		var curRowIdx int
		var rowCells []string
		var cellText strings.Builder
		cellRepeat := 1
		inCell, inP := false, false

		flushSheet := func() {
			if curSheet != nil {
				if opts.preserveEmptyRows {
					for len(curSheet.Rows) > 0 && len(curSheet.Rows[len(curSheet.Rows)-1]) == 0 {
						curSheet.Rows = curSheet.Rows[:len(curSheet.Rows)-1]
						curSheet.TotalRows = len(curSheet.Rows)
					}
				}
				if len(curSheet.Rows) > 0 || opts.preserveEmptyRows {
					if !opts.preserveEmptyRows && hasNonSequentialRowNumbers(curRowNums) {
						curSheet.RowNumbers = curRowNums
					}
					sheets = append(sheets, *curSheet)
				}
			}
			curSheet = nil
			curRowNums = nil
			curRowIdx = 0
		}

		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch se := tok.(type) {
			case xml.StartElement:
				switch se.Name.Local {
				case "table":
					flushSheet()
					name := fmt.Sprintf("Sheet%d", len(sheets)+1)
					for _, attr := range se.Attr {
						if attr.Name.Local == "name" && attr.Value != "" {
							name = attr.Value
						}
					}
					curSheet = &SpreadsheetSheet{Name: name}
				case "table-row":
					rowCells = nil
				case "table-cell":
					inCell = true
					cellText.Reset()
					cellRepeat = 1
					for _, attr := range se.Attr {
						if attr.Name.Local == "number-columns-repeated" {
							if n, err := strconv.Atoi(attr.Value); err == nil && n > 1 && n <= 64 {
								cellRepeat = n
							}
						}
					}
				case "p":
					if inCell {
						inP = true
					}
				}
			case xml.CharData:
				if inCell && inP {
					cellText.Write(se)
				}
			case xml.EndElement:
				switch se.Name.Local {
				case "p":
					inP = false
				case "table-cell":
					if inCell {
						val := cellText.String()
						if !opts.preserveExactCells {
							val = sanitizeCellText(val)
						}
						for r := 0; r < cellRepeat; r++ {
							rowCells = append(rowCells, val)
						}
						inCell = false
					}
				case "table-row":
					if curSheet == nil {
						curSheet = &SpreadsheetSheet{Name: "Sheet1"}
					}
					curRowIdx++
					trimmed := trimTrailingEmptyCells(append([]string(nil), rowCells...))
					if len(trimmed) > 0 || opts.preserveEmptyRows {
						curSheet.TotalRows++
						if opts.maxRows <= 0 || len(curSheet.Rows) < opts.maxRows {
							curSheet.Rows = append(curSheet.Rows, trimmed)
							curRowNums = append(curRowNums, curRowIdx)
						} else {
							curSheet.Truncated = true
						}
					}
				case "table":
					flushSheet()
				}
			}
		}
		flushSheet()
		return sheets, nil
	}
	return nil, os.ErrNotExist
}

func parseODSFile(odsPath string, maxRows int) ([]SpreadsheetSheet, error) {
	return parseODSFileWithOpts(odsPath, spreadsheetParseOptions{maxRows: maxRows})
}

func parseXMLSpreadsheet2003(data []byte, maxRows int) []SpreadsheetSheet {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var sheets []SpreadsheetSheet
	var curSheet *SpreadsheetSheet
	var curRowNums []int
	var curRowIdx int
	var rowCells []string
	var cellData strings.Builder
	inCell, inData := false, false
	nextColIdx := 0

	flushSheet := func() {
		if curSheet != nil && len(curSheet.Rows) > 0 {
			if hasNonSequentialRowNumbers(curRowNums) {
				curSheet.RowNumbers = curRowNums
			}
			sheets = append(sheets, *curSheet)
		}
		curSheet = nil
		curRowNums = nil
		curRowIdx = 0
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "Worksheet":
				flushSheet()
				name := fmt.Sprintf("Sheet%d", len(sheets)+1)
				for _, attr := range se.Attr {
					if attr.Name.Local == "Name" && attr.Value != "" {
						name = attr.Value
					}
				}
				curSheet = &SpreadsheetSheet{Name: name}
			case "Row":
				rowCells = nil
				nextColIdx = 0
				curRowIdx++
				for _, attr := range se.Attr {
					if attr.Name.Local == "Index" {
						if idx, err := strconv.Atoi(attr.Value); err == nil && idx > 0 && idx <= 1048576 {
							curRowIdx = idx
						}
					}
				}
			case "Cell":
				inCell = true
				cellData.Reset()
				for _, attr := range se.Attr {
					if attr.Name.Local == "Index" {
						if idx, err := strconv.Atoi(attr.Value); err == nil && idx > 0 && idx <= 16384 {
							nextColIdx = idx - 1
						}
					}
				}
			case "Data":
				if inCell {
					inData = true
				}
			}
		case xml.CharData:
			if inCell && inData {
				cellData.Write(se)
			}
		case xml.EndElement:
			switch se.Name.Local {
			case "Data":
				inData = false
			case "Cell":
				if inCell {
					for len(rowCells) <= nextColIdx {
						rowCells = append(rowCells, "")
					}
					rowCells[nextColIdx] = sanitizeCellText(cellData.String())
					nextColIdx++
					inCell = false
				}
			case "Row":
				if curSheet == nil {
					curSheet = &SpreadsheetSheet{Name: "Sheet1"}
				}
				trimmed := trimTrailingEmptyCells(rowCells)
				if len(trimmed) > 0 {
					curSheet.TotalRows++
					if maxRows <= 0 || len(curSheet.Rows) < maxRows {
						curSheet.Rows = append(curSheet.Rows, trimmed)
						curRowNums = append(curRowNums, curRowIdx)
					} else {
						curSheet.Truncated = true
					}
				}
			case "Worksheet":
				flushSheet()
			}
		}
	}
	flushSheet()
	return sheets
}

func parseHTMLTableSpreadsheet(data []byte, defaultName string, maxRows int) []SpreadsheetSheet {
	text := string(data)
	rowMatches := htmlRowRe.FindAllStringSubmatch(text, -1)
	if len(rowMatches) == 0 {
		return nil
	}
	var rows [][]string
	totalRows := 0
	truncated := false
	for _, rm := range rowMatches {
		cellMatches := htmlCellRe.FindAllStringSubmatch(rm[1], -1)
		if len(cellMatches) == 0 {
			continue
		}
		var cols []string
		hasVal := false
		for _, cm := range cellMatches {
			stripped := htmlTagStrip.ReplaceAllString(cm[1], " ")
			val := sanitizeCellText(html.UnescapeString(stripped))
			if val != "" {
				hasVal = true
			}
			cols = append(cols, val)
		}
		if !hasVal {
			continue
		}
		cols = trimTrailingEmptyCells(cols)
		totalRows++
		if maxRows <= 0 || len(rows) < maxRows {
			rows = append(rows, cols)
		} else {
			truncated = true
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if defaultName == "" {
		defaultName = "Sheet1"
	}
	return []SpreadsheetSheet{{
		Name:      defaultName,
		Rows:      rows,
		TotalRows: totalRows,
		Truncated: truncated,
	}}
}

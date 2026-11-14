package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func resolveSafeDocPath(rawPath, cwd string) (string, error) {
	cleaned := strings.TrimSpace(rawPath)
	if cleaned == "" {
		return "", fmt.Errorf("file path is required")
	}
	if !filepath.IsAbs(cleaned) {
		base := strings.TrimSpace(cwd)
		if base == "" {
			base, _ = os.Getwd()
		}
		cleaned = filepath.Join(base, cleaned)
	}
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	cleaned = filepath.Clean(cleaned)
	checkPath := cleaned
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && resolved != "" {
		checkPath = filepath.Clean(resolved)
	} else if parentResolved, pErr := filepath.EvalSymlinks(filepath.Dir(cleaned)); pErr == nil && parentResolved != "" {
		checkPath = filepath.Join(filepath.Clean(parentResolved), filepath.Base(cleaned))
	}

	if isDeniedDocPath(cleaned) || isDeniedDocPath(checkPath) {
		return "", fmt.Errorf("access to protected or credential path %q is forbidden", rawPath)
	}
	return cleaned, nil
}

func isDeniedDocPath(p string) bool {
	return hoxipaths.IsDeniedDocOrSpreadsheetPath(p)
}

func runDocCommand(args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) int {
	action := "inspect"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	if action == "help" || action == "-h" || action == "--help" {
		printDocHelp(stdout)
		return 0
	}

	fs := flag.NewFlagSet("hoxi doc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dir        string
		filePath   string
		outPath    string
		sheetName  string
		format     string
		title      string
		fromPath   string
		fromJSON   string
		fromCSV    string
		fromTSV    string
		rowsJSON   string
		appendJSON string
		textFlag   string
		maxRows    int
		maxChars   int
	)
	fs.StringVar(&dir, "dir", cwd, "workspace directory")
	fs.StringVar(&filePath, "file", "", "target document file path")
	fs.StringVar(&filePath, "f", "", "shorthand for --file")
	fs.StringVar(&outPath, "out", "", "output file path (for set-cell)")
	fs.StringVar(&outPath, "o", "", "shorthand for --out")
	fs.StringVar(&sheetName, "sheet", "", "target worksheet name")
	fs.StringVar(&format, "format", "text", "output format for read: text | md | csv | tsv | json")
	fs.StringVar(&title, "title", "", "document title (for write-docx / write-pdf)")
	fs.StringVar(&fromPath, "from", "", "input text/Markdown file path or '-' for stdin")
	fs.StringVar(&fromJSON, "from-json", "", "input JSON file path or '-' for stdin (for write-xlsx)")
	fs.StringVar(&fromCSV, "from-csv", "", "input CSV file path or '-' for stdin (for write-xlsx)")
	fs.StringVar(&fromTSV, "from-tsv", "", "input TSV file path or '-' for stdin (for write-xlsx)")
	fs.StringVar(&rowsJSON, "rows-json", "", "inline JSON rows or sheets (for write-xlsx)")
	fs.StringVar(&appendJSON, "append-json", "", "inline JSON 2D array of rows to append (for set-cell)")
	fs.StringVar(&textFlag, "text", "", "inline text/Markdown content (for write-docx / write-pdf)")
	fs.IntVar(&maxRows, "max-rows", 400, "maximum rows per sheet to read")
	fs.IntVar(&maxChars, "max-chars", 64000, "maximum characters to extract")
	posArgs, err := parseFlagsInterspersed(fs, args)
	if err != nil {
		return 2
	}

	if filePath == "" && len(posArgs) > 0 {
		filePath = posArgs[0]
		posArgs = posArgs[1:]
	}

	targetPath, err := resolveSafeDocPath(filePath, dir)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	switch action {
	case "inspect":
		return runDocInspect(targetPath, maxRows, stdout, stderr)
	case "read", "extract":
		return runDocRead(targetPath, sheetName, format, maxRows, maxChars, stdout, stderr)
	case "write-xlsx":
		return runDocWriteXLSX(targetPath, sheetName, fromJSON, fromCSV, fromTSV, rowsJSON, dir, stdin, stdout, stderr)
	case "set-cell", "edit-xlsx":
		effOut := targetPath
		if strings.TrimSpace(outPath) != "" {
			resolvedOut, outErr := resolveSafeDocPath(outPath, dir)
			if outErr != nil {
				fmt.Fprintf(stderr, "Error: %v\n", outErr)
				return 1
			}
			effOut = resolvedOut
		}
		return runDocSetCell(targetPath, effOut, sheetName, appendJSON, posArgs, stdout, stderr)
	case "write-docx":
		return runDocWriteDOCX(targetPath, title, fromPath, textFlag, dir, stdin, stdout, stderr)
	case "write-pptx":
		return runDocWritePPTX(targetPath, title, fromPath, textFlag, dir, stdin, stdout, stderr)
	case "write-pdf":
		return runDocWritePDF(targetPath, title, fromPath, textFlag, dir, stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Unknown doc action %q (supported: inspect, read, write-xlsx, set-cell, write-docx, write-pptx, write-pdf)\n", action)
		return 2
	}
}

func runDocInspect(targetPath string, maxRows int, stdout, stderr io.Writer) int {
	info, err := os.Stat(targetPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if info.IsDir() {
		fmt.Fprintf(stderr, "Error: %s is a directory\n", targetPath)
		return 1
	}
	ext := strings.ToLower(filepath.Ext(targetPath))
	kind, mime, _ := attachment.ClassifyFile(filepath.Base(targetPath), "")

	type sheetSummary struct {
		Name      string   `json:"name"`
		Rows      int      `json:"rows"`
		Columns   int      `json:"columns"`
		Headers   []string `json:"headers,omitempty"`
		Truncated bool     `json:"truncated,omitempty"`
	}
	resp := map[string]any{
		"path":     targetPath,
		"filename": filepath.Base(targetPath),
		"ext":      ext,
		"kind":     kind,
		"mime":     mime,
		"size":     info.Size(),
	}

	if kind == attachment.KindSpreadsheet || attachment.IsSpreadsheetExt(ext) {
		sheets, sErr := attachment.ParseSpreadsheetFile(targetPath, maxRows)
		if sErr != nil {
			fmt.Fprintf(stderr, "Error parsing spreadsheet: %v\n", sErr)
			return 1
		}
		summaries := make([]sheetSummary, 0, len(sheets))
		for _, s := range sheets {
			maxCols := 0
			for _, r := range s.Rows {
				if len(r) > maxCols {
					maxCols = len(r)
				}
			}
			var headers []string
			if len(s.Rows) > 0 {
				headers = s.Rows[0]
			}
			rowCount := s.TotalRows
			if rowCount == 0 {
				rowCount = len(s.Rows)
			}
			summaries = append(summaries, sheetSummary{
				Name:      s.Name,
				Rows:      rowCount,
				Columns:   maxCols,
				Headers:   headers,
				Truncated: s.Truncated,
			})
		}
		resp["sheets"] = summaries
	} else {
		text := attachment.ExtractFileText(targetPath, 64000)
		lines := 0
		if text != "" {
			lines = strings.Count(text, "\n") + 1
		}
		resp["chars"] = len(text)
		resp["lines"] = lines
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
	return 0
}

func runDocRead(targetPath, sheetFilter, format string, maxRows, maxChars int, stdout, stderr io.Writer) int {
	if _, err := os.Stat(targetPath); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	ext := strings.ToLower(filepath.Ext(targetPath))
	isSheet := attachment.IsSpreadsheetExt(ext)
	fmtLower := strings.ToLower(strings.TrimSpace(format))

	if isSheet {
		sheets, err := attachment.ParseSpreadsheetFile(targetPath, maxRows)
		if err != nil {
			fmt.Fprintf(stderr, "Error reading spreadsheet: %v\n", err)
			return 1
		}
		if trimmed := strings.TrimSpace(sheetFilter); trimmed != "" {
			var filtered []attachment.SpreadsheetSheet
			for _, s := range sheets {
				if strings.EqualFold(s.Name, trimmed) {
					filtered = append(filtered, s)
				}
			}
			sheets = filtered
		}
		switch fmtLower {
		case "json":
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]any{"sheets": sheets})
			return 0
		case "csv", "tsv":
			var buf bytes.Buffer
			w := csv.NewWriter(&buf)
			if fmtLower == "tsv" {
				w.Comma = '\t'
			}
			for _, s := range sheets {
				for _, r := range s.Rows {
					_ = w.Write(r)
				}
			}
			w.Flush()
			_, _ = stdout.Write(buf.Bytes())
			return 0
		case "md", "markdown":
			_, _ = io.WriteString(stdout, formatSheetsAsMarkdown(sheets)+"\n")
			return 0
		default:
			_, _ = io.WriteString(stdout, attachment.FormatSpreadsheetSheets(sheets)+"\n")
			return 0
		}
	}

	text := attachment.ExtractFileText(targetPath, maxChars)
	if fmtLower == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"path":    targetPath,
			"content": text,
		})
		return 0
	}
	_, _ = io.WriteString(stdout, text+"\n")
	return 0
}

func formatSheetsAsMarkdown(sheets []attachment.SpreadsheetSheet) string {
	var out strings.Builder
	for _, s := range sheets {
		if len(s.Rows) == 0 {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString("### " + s.Name + "\n\n")
		maxCols := 0
		for _, r := range s.Rows {
			if len(r) > maxCols {
				maxCols = len(r)
			}
		}
		if maxCols == 0 {
			continue
		}
		for rIdx, r := range s.Rows {
			out.WriteString("|")
			for cIdx := 0; cIdx < maxCols; cIdx++ {
				val := ""
				if cIdx < len(r) {
					val = strings.ReplaceAll(r[cIdx], "|", "\\|")
				}
				out.WriteString(" " + val + " |")
			}
			out.WriteByte('\n')
			if rIdx == 0 {
				out.WriteString("|")
				for cIdx := 0; cIdx < maxCols; cIdx++ {
					out.WriteString(" --- |")
				}
				out.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(out.String())
}

func readInputSource(srcPath, inlineText, cwd string, stdin io.Reader) ([]byte, error) {
	if strings.TrimSpace(inlineText) != "" {
		return []byte(inlineText), nil
	}
	trimmed := strings.TrimSpace(srcPath)
	if trimmed == "-" {
		if stdin == nil {
			return nil, fmt.Errorf("stdin is empty")
		}
		return io.ReadAll(io.LimitReader(stdin, 16*1024*1024))
	}
	if trimmed != "" {
		safeSrc, err := resolveSafeDocPath(trimmed, cwd)
		if err != nil {
			return nil, err
		}
		return os.ReadFile(safeSrc)
	}
	return nil, fmt.Errorf("input content is required (use --from, --text, or source flags)")
}

func parseJSONSheetsPayload(raw []byte, defaultSheetName string) ([]attachment.SpreadsheetSheet, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty JSON payload")
	}
	var wrapped struct {
		Sheets []attachment.SpreadsheetSheet `json:"sheets"`
	}
	if err := json.Unmarshal(trimmed, &wrapped); err == nil && len(wrapped.Sheets) > 0 {
		return wrapped.Sheets, nil
	}
	var sheets []attachment.SpreadsheetSheet
	if err := json.Unmarshal(trimmed, &sheets); err == nil && len(sheets) > 0 && (sheets[0].Name != "" || sheets[0].Rows != nil) {
		return sheets, nil
	}
	var rawRows [][]any
	if err := json.Unmarshal(trimmed, &rawRows); err == nil {
		rows := make([][]string, 0, len(rawRows))
		for _, r := range rawRows {
			row := make([]string, len(r))
			for i, cell := range r {
				if cell == nil {
					row[i] = ""
				} else {
					row[i] = fmt.Sprint(cell)
				}
			}
			rows = append(rows, row)
		}
		name := strings.TrimSpace(defaultSheetName)
		if name == "" {
			name = "Sheet1"
		}
		return []attachment.SpreadsheetSheet{{Name: name, Rows: rows, TotalRows: len(rows)}}, nil
	}
	return nil, fmt.Errorf("invalid JSON spreadsheet payload (expected [{\"name\":\"Sheet1\",\"rows\":[[...]]}] or [[\"col1\",\"col2\"], ...])")
}

func runDocWriteXLSX(
	targetPath, sheetName, fromJSON, fromCSV, fromTSV, rowsJSON, cwd string,
	stdin io.Reader, stdout, stderr io.Writer,
) int {
	var sheets []attachment.SpreadsheetSheet
	switch {
	case strings.TrimSpace(rowsJSON) != "" || strings.TrimSpace(fromJSON) != "":
		raw, err := readInputSource(fromJSON, rowsJSON, cwd, stdin)
		if err != nil {
			fmt.Fprintf(stderr, "Error reading JSON input: %v\n", err)
			return 1
		}
		parsed, err := parseJSONSheetsPayload(raw, sheetName)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		sheets = parsed
	case strings.TrimSpace(fromCSV) != "" || strings.TrimSpace(fromTSV) != "":
		src := fromCSV
		comma := ','
		if strings.TrimSpace(fromTSV) != "" {
			src = fromTSV
			comma = '\t'
		}
		raw, err := readInputSource(src, "", cwd, stdin)
		if err != nil {
			fmt.Fprintf(stderr, "Error reading delimited input: %v\n", err)
			return 1
		}
		r := csv.NewReader(bytes.NewReader(raw))
		r.Comma = comma
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		rows, err := r.ReadAll()
		if err != nil {
			fmt.Fprintf(stderr, "Error parsing delimited input: %v\n", err)
			return 1
		}
		name := strings.TrimSpace(sheetName)
		if name == "" {
			name = "Sheet1"
		}
		sheets = []attachment.SpreadsheetSheet{{Name: name, Rows: rows, TotalRows: len(rows)}}
	default:
		fmt.Fprintln(stderr, "Error: write-xlsx requires --rows-json, --from-json, --from-csv, or --from-tsv")
		return 2
	}

	if err := attachment.WriteXLSXFile(targetPath, sheets); err != nil {
		fmt.Fprintf(stderr, "Error writing %s: %v\n", targetPath, err)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"ok":     true,
		"path":   targetPath,
		"sheets": len(sheets),
	})
	return 0
}

func runDocSetCell(
	inPath, outPath, sheetName, appendJSON string,
	assignments []string,
	stdout, stderr io.Writer,
) int {
	edits := make(map[string]string)
	for _, item := range assignments {
		eq := strings.IndexByte(item, '=')
		if eq <= 0 {
			fmt.Fprintf(stderr, "Error: invalid cell assignment %q (expected Cell=Value like B2=1280)\n", item)
			return 2
		}
		ref := strings.ToUpper(strings.TrimSpace(item[:eq]))
		val := item[eq+1:]
		edits[ref] = val
	}

	var appendRows [][]string
	if strings.TrimSpace(appendJSON) != "" {
		parsed, err := parseJSONSheetsPayload([]byte(appendJSON), sheetName)
		if err != nil || len(parsed) == 0 {
			fmt.Fprintf(stderr, "Error parsing --append-json: %v\n", err)
			return 2
		}
		appendRows = parsed[0].Rows
	}

	if len(edits) == 0 && len(appendRows) == 0 {
		fmt.Fprintln(stderr, "Error: set-cell requires at least one Cell=Value argument or --append-json")
		return 2
	}

	sheets, err := attachment.ApplySpreadsheetEdits(inPath, outPath, sheetName, edits, appendRows)
	if err != nil {
		fmt.Fprintf(stderr, "Error updating spreadsheet: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"ok":           true,
		"path":         outPath,
		"updatedCells": len(edits),
		"appendedRows": len(appendRows),
		"sheets":       len(sheets),
	})
	return 0
}

func runDocWriteDOCX(targetPath, title, fromPath, inlineText, cwd string, stdin io.Reader, stdout, stderr io.Writer) int {
	raw, err := readInputSource(fromPath, inlineText, cwd, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	if err := attachment.WriteDOCXFile(targetPath, title, string(raw)); err != nil {
		fmt.Fprintf(stderr, "Error writing .docx: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"ok":   true,
		"path": targetPath,
	})
	return 0
}

func runDocWritePPTX(targetPath, title, fromPath, inlineText, cwd string, stdin io.Reader, stdout, stderr io.Writer) int {
	raw, err := readInputSource(fromPath, inlineText, cwd, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	if err := attachment.WritePPTXFile(targetPath, title, string(raw)); err != nil {
		fmt.Fprintf(stderr, "Error writing .pptx: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"ok":   true,
		"path": targetPath,
	})
	return 0
}

func runDocWritePDF(targetPath, title, fromPath, inlineText, cwd string, stdin io.Reader, stdout, stderr io.Writer) int {
	raw, err := readInputSource(fromPath, inlineText, cwd, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	if err := attachment.WritePDFFile(targetPath, title, string(raw)); err != nil {
		fmt.Fprintf(stderr, "Error writing .pdf: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"ok":   true,
		"path": targetPath,
	})
	return 0
}

func printDocHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  hoxi doc inspect <file>                                     Inspect document/spreadsheet metadata & sheets
  hoxi doc read <file> [--sheet <name>] [--format text|md|csv|json]
                                                              Extract text or structured rows from PDF/Excel/Word/PPT
  hoxi doc write-xlsx <out.xlsx> [--sheet <name>] (--rows-json <json> | --from-json <file> | --from-csv <file>)
                                                              Create an .xlsx spreadsheet without Python
  hoxi doc set-cell <file.xlsx> [--sheet <name>] [--out <out.xlsx>] [A1=Value ...] [--append-json <rows>]
                                                              Update cells or append rows in an .xlsx/.xls/.csv file
  hoxi doc write-docx <out.docx> [--title <title>] (--from <md-file> | --text <markdown>)
                                                              Generate a Word (.docx) document from Markdown
  hoxi doc write-pptx <out.pptx> [--title <title>] (--from <md-file> | --text <markdown>)
                                                              Generate a PowerPoint (.pptx) presentation from Markdown
  hoxi doc write-pdf <out.pdf> [--title <title>] (--from <file> | --text <content>)
                                                              Generate a PDF document from text/Markdown
`)
}

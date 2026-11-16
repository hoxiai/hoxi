package attachment

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

// extractPDFTextPureGo extracts text literals from uncompressed and FlateDecode
// PDF content streams without requiring external tools.
func extractPDFTextPureGo(pdfPath string) string {
	raw, err := os.ReadFile(pdfPath)
	if err != nil || len(raw) == 0 {
		return ""
	}
	var out strings.Builder
	search := raw
	for len(search) > 0 {
		idx := bytes.Index(search, []byte("stream"))
		if idx < 0 {
			break
		}
		headerStart := idx - 256
		if headerStart < 0 {
			headerStart = 0
		}
		dictSlice := search[headerStart:idx]
		afterStream := search[idx+len("stream"):]
		if len(afterStream) > 0 && afterStream[0] == '\r' {
			afterStream = afterStream[1:]
		}
		if len(afterStream) == 0 || afterStream[0] != '\n' {
			search = search[idx+len("stream"):]
			continue
		}
		afterStream = afterStream[1:]
		endIdx := bytes.Index(afterStream, []byte("endstream"))
		if endIdx < 0 {
			break
		}
		streamData := bytes.TrimRight(afterStream[:endIdx], "\r\n")
		search = afterStream[endIdx+len("endstream"):]

		content := streamData
		if bytes.Contains(dictSlice, []byte("/FlateDecode")) {
			zr, err := zlib.NewReader(bytes.NewReader(streamData))
			if err != nil {
				continue
			}
			decomp, err := io.ReadAll(io.LimitReader(zr, 8*1024*1024))
			_ = zr.Close()
			if err != nil || len(decomp) == 0 {
				continue
			}
			content = decomp
		}
		if !bytes.Contains(content, []byte("BT")) {
			continue
		}
		extractPDFTextObjects(content, &out)
		if out.Len() >= 128*1024 {
			break
		}
	}
	return strings.TrimSpace(out.String())
}

func extractPDFTextObjects(content []byte, out *strings.Builder) {
	rest := content
	for {
		bt := bytes.Index(rest, []byte("BT"))
		if bt < 0 {
			return
		}
		rest = rest[bt+2:]
		et := bytes.Index(rest, []byte("ET"))
		var block []byte
		if et >= 0 {
			block = rest[:et]
			rest = rest[et+2:]
		} else {
			block = rest
			rest = nil
		}
		var line strings.Builder
		i := 0
		for i < len(block) {
			switch block[i] {
			case '(':
				lit, next := parsePDFLiteralString(block, i)
				i = next
				line.WriteString(lit)
			case '\n', '\r':
				i++
			case 'T':
				if i+1 < len(block) && (block[i+1] == 'd' || block[i+1] == 'D' || block[i+1] == '*') {
					if line.Len() > 0 {
						out.WriteString(strings.TrimSpace(line.String()))
						out.WriteByte('\n')
						line.Reset()
					}
					i += 2
					continue
				}
				i++
			default:
				i++
			}
		}
		if s := strings.TrimSpace(line.String()); s != "" {
			out.WriteString(s)
			out.WriteByte('\n')
		}
		if len(rest) == 0 {
			return
		}
	}
}

func parsePDFLiteralString(buf []byte, start int) (string, int) {
	if start >= len(buf) || buf[start] != '(' {
		return "", start + 1
	}
	var raw []byte
	depth := 1
	i := start + 1
	for i < len(buf) && depth > 0 {
		ch := buf[i]
		if ch == '\\' {
			i++
			if i >= len(buf) {
				break
			}
			esc := buf[i]
			switch esc {
			case 'n':
				raw = append(raw, '\n')
			case 'r':
				raw = append(raw, '\r')
			case 't':
				raw = append(raw, '\t')
			case 'b':
				raw = append(raw, '\b')
			case 'f':
				raw = append(raw, '\f')
			case '(', ')', '\\':
				raw = append(raw, esc)
			default:
				if esc >= '0' && esc <= '7' {
					val := int(esc - '0')
					for k := 0; k < 2 && i+1 < len(buf) && buf[i+1] >= '0' && buf[i+1] <= '7'; k++ {
						i++
						val = val*8 + int(buf[i]-'0')
					}
					raw = append(raw, byte(val))
				} else {
					raw = append(raw, esc)
				}
			}
			i++
			continue
		}
		if ch == '(' {
			depth++
			raw = append(raw, ch)
		} else if ch == ')' {
			depth--
			if depth > 0 {
				raw = append(raw, ch)
			}
		} else {
			raw = append(raw, ch)
		}
		i++
	}
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		u16 := make([]uint16, 0, (len(raw)-2)/2)
		for j := 2; j+1 < len(raw); j += 2 {
			u16 = append(u16, uint16(raw[j])<<8|uint16(raw[j+1]))
		}
		return string(utf16.Decode(u16)), i
	}
	return string(raw), i
}

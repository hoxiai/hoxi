package attachment

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	ole2Signature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	zipSignature  = []byte{'P', 'K', 0x03, 0x04}
)

func parseXLSFile(xlsPath string, maxRows int) ([]SpreadsheetSheet, error) {
	f, err := os.Open(xlsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, 16*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	if bytes.HasPrefix(data, zipSignature) {
		return parseXLSXFile(xlsPath, maxRows)
	}
	if bytes.HasPrefix(data, ole2Signature) {
		stream, err := extractOLE2WorkbookStream(data)
		if err != nil {
			return nil, err
		}
		return parseBIFF8Workbook(stream, maxRows)
	}

	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))
	prefixLower := strings.ToLower(string(trimmed[:minInt(len(trimmed), 512)]))
	if strings.Contains(prefixLower, "<workbook") || strings.Contains(prefixLower, "<worksheet") || strings.Contains(prefixLower, "urn:schemas-microsoft-com:office:spreadsheet") {
		if sheets := parseXMLSpreadsheet2003(trimmed, maxRows); len(sheets) > 0 {
			return sheets, nil
		}
	}
	if strings.Contains(prefixLower, "<table") || strings.Contains(prefixLower, "<html") || strings.Contains(prefixLower, "<!doctype html") {
		base := strings.TrimSuffix(filepath.Base(xlsPath), filepath.Ext(xlsPath))
		if sheets := parseHTMLTableSpreadsheet(trimmed, base, maxRows); len(sheets) > 0 {
			return sheets, nil
		}
	}
	if utf8.Valid(trimmed) && !bytes.Contains(trimmed, []byte{0}) {
		base := strings.TrimSuffix(filepath.Base(xlsPath), filepath.Ext(xlsPath))
		return parseDelimitedBytes(trimmed, 0, base, maxRows), nil
	}
	return nil, fmt.Errorf("unsupported .xls format")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const (
	oleFreeSect   = 0xFFFFFFFF
	oleEndOfChain = 0xFFFFFFFE
)

func extractOLE2WorkbookStream(data []byte) ([]byte, error) {
	if len(data) < 512 || !bytes.HasPrefix(data, ole2Signature) {
		return nil, fmt.Errorf("invalid OLE2 header")
	}
	sectorShift := binary.LittleEndian.Uint16(data[30:32])
	if sectorShift < 7 || sectorShift > 16 {
		sectorShift = 9
	}
	sectorSize := 1 << int(sectorShift)
	miniShift := binary.LittleEndian.Uint16(data[32:34])
	if miniShift < 4 || miniShift > 12 {
		miniShift = 6
	}
	miniSectorSize := 1 << int(miniShift)

	firstDirSect := binary.LittleEndian.Uint32(data[48:52])
	miniCutoff := binary.LittleEndian.Uint32(data[56:60])
	if miniCutoff == 0 {
		miniCutoff = 4096
	}
	firstMiniFATSect := binary.LittleEndian.Uint32(data[60:64])
	numMiniFATSects := binary.LittleEndian.Uint32(data[64:68])
	firstDIFATSect := binary.LittleEndian.Uint32(data[68:72])
	numDIFATSects := binary.LittleEndian.Uint32(data[72:76])

	readSector := func(secID uint32) ([]byte, bool) {
		offset := int64(secID+1) * int64(sectorSize)
		if offset < 512 || offset+int64(sectorSize) > int64(len(data)) {
			return nil, false
		}
		return data[offset : offset+int64(sectorSize)], true
	}

	var fatSectors []uint32
	for i := 0; i < 109; i++ {
		pos := 76 + i*4
		sec := binary.LittleEndian.Uint32(data[pos : pos+4])
		if sec != oleFreeSect && sec != oleEndOfChain {
			fatSectors = append(fatSectors, sec)
		}
	}
	difatSec := firstDIFATSect
	for i := uint32(0); i < numDIFATSects && i < 1024 && difatSec != oleFreeSect && difatSec != oleEndOfChain; i++ {
		blk, ok := readSector(difatSec)
		if !ok || len(blk) < 4 {
			break
		}
		entriesPerDIFAT := (len(blk) / 4) - 1
		for j := 0; j < entriesPerDIFAT; j++ {
			sec := binary.LittleEndian.Uint32(blk[j*4 : j*4+4])
			if sec != oleFreeSect && sec != oleEndOfChain {
				fatSectors = append(fatSectors, sec)
			}
		}
		difatSec = binary.LittleEndian.Uint32(blk[len(blk)-4:])
	}

	entriesPerSector := sectorSize / 4
	fat := make([]uint32, 0, len(fatSectors)*entriesPerSector)
	for _, sec := range fatSectors {
		blk, ok := readSector(sec)
		if !ok {
			continue
		}
		for i := 0; i+4 <= len(blk); i += 4 {
			fat = append(fat, binary.LittleEndian.Uint32(blk[i:i+4]))
		}
	}

	readChain := func(startSec uint32, maxBytes int) []byte {
		if startSec == oleFreeSect || startSec == oleEndOfChain {
			return nil
		}
		var out bytes.Buffer
		cur := startSec
		steps := 0
		maxSteps := len(fat) + 16
		for cur != oleFreeSect && cur != oleEndOfChain && steps < maxSteps {
			blk, ok := readSector(cur)
			if !ok {
				break
			}
			out.Write(blk)
			if maxBytes > 0 && out.Len() >= maxBytes {
				break
			}
			if int(cur) >= len(fat) {
				break
			}
			cur = fat[cur]
			steps++
		}
		res := out.Bytes()
		if maxBytes > 0 && len(res) > maxBytes {
			res = res[:maxBytes]
		}
		return res
	}

	dirBytes := readChain(firstDirSect, 4*1024*1024)
	if len(dirBytes) < 128 {
		return nil, fmt.Errorf("empty OLE2 directory")
	}

	type oleDirEntry struct {
		name      string
		objType   byte
		startSec  uint32
		size      uint64
	}
	var rootEntry *oleDirEntry
	var wbEntry *oleDirEntry

	for off := 0; off+128 <= len(dirBytes); off += 128 {
		ent := dirBytes[off : off+128]
		objType := ent[66]
		if objType == 0 {
			continue
		}
		nameByteLen := int(binary.LittleEndian.Uint16(ent[64:66]))
		if nameByteLen > 64 {
			nameByteLen = 64
		}
		var u16s []uint16
		for i := 0; i+2 <= nameByteLen; i += 2 {
			ch := binary.LittleEndian.Uint16(ent[i : i+2])
			if ch == 0 {
				break
			}
			u16s = append(u16s, ch)
		}
		name := string(utf16.Decode(u16s))
		startSec := binary.LittleEndian.Uint32(ent[116:120])
		size := binary.LittleEndian.Uint64(ent[120:128])
		if sectorShift == 9 {
			size = uint64(binary.LittleEndian.Uint32(ent[120:124]))
		}
		de := oleDirEntry{name: name, objType: objType, startSec: startSec, size: size}
		if objType == 5 && rootEntry == nil {
			cp := de
			rootEntry = &cp
		}
		if objType == 2 && (strings.EqualFold(name, "Workbook") || strings.EqualFold(name, "Book")) {
			cp := de
			wbEntry = &cp
			if strings.EqualFold(name, "Workbook") {
				break
			}
		}
	}

	if wbEntry == nil {
		return nil, fmt.Errorf("OLE2 Workbook stream not found")
	}
	if wbEntry.size == 0 {
		return nil, nil
	}
	maxStream := int(minUint64(wbEntry.size, 32*1024*1024))
	if wbEntry.size >= uint64(miniCutoff) || rootEntry == nil {
		return readChain(wbEntry.startSec, maxStream), nil
	}

	// Read from MiniFAT + Root Storage mini-stream
	miniStream := readChain(rootEntry.startSec, int(minUint64(rootEntry.size, 32*1024*1024)))
	miniFATBytes := readChain(firstMiniFATSect, int(numMiniFATSects)*sectorSize)
	var miniFAT []uint32
	for i := 0; i+4 <= len(miniFATBytes); i += 4 {
		miniFAT = append(miniFAT, binary.LittleEndian.Uint32(miniFATBytes[i:i+4]))
	}

	var out bytes.Buffer
	cur := wbEntry.startSec
	steps := 0
	for cur != oleFreeSect && cur != oleEndOfChain && out.Len() < maxStream && steps < len(miniFAT)+64 {
		off := int(cur) * miniSectorSize
		if off < 0 || off+miniSectorSize > len(miniStream) {
			break
		}
		out.Write(miniStream[off : off+miniSectorSize])
		if int(cur) >= len(miniFAT) {
			break
		}
		cur = miniFAT[cur]
		steps++
	}
	res := out.Bytes()
	if len(res) > maxStream {
		res = res[:maxStream]
	}
	return res, nil
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

const (
	biffRecBOF        = 0x0809
	biffRecBOF4       = 0x0409
	biffRecBOF2       = 0x0209
	biffRecEOF        = 0x000A
	biffRecBoundSheet = 0x0085
	biffRecSST        = 0x00FC
	biffRecContinue   = 0x003C
	biffRecLabelSST   = 0x00FD
	biffRecLabel      = 0x0204
	biffRecRString    = 0x00D6
	biffRecRK         = 0x027E
	biffRecMulRK      = 0x00BD
	biffRecNumber     = 0x0203
	biffRecBoolErr    = 0x0205
	biffRecFormula    = 0x0006
	biffRecString     = 0x0207
)

type biffRecord struct {
	id   uint16
	data []byte
}

func parseBIFF8Workbook(stream []byte, maxRows int) ([]SpreadsheetSheet, error) {
	if len(stream) < 4 {
		return nil, nil
	}
	var records []biffRecord
	for pos := 0; pos+4 <= len(stream); {
		recID := binary.LittleEndian.Uint16(stream[pos : pos+2])
		recLen := int(binary.LittleEndian.Uint16(stream[pos+2 : pos+4]))
		pos += 4
		if pos+recLen > len(stream) {
			recLen = len(stream) - pos
		}
		records = append(records, biffRecord{id: recID, data: stream[pos : pos+recLen]})
		pos += recLen
	}

	var sheetNames []string
	var sst []string
	for i := 0; i < len(records); i++ {
		rec := records[i]
		switch rec.id {
		case biffRecBoundSheet:
			if len(rec.data) >= 7 {
				name := decodeBIFF8ShortString(rec.data[6:])
				if strings.TrimSpace(name) == "" {
					name = fmt.Sprintf("Sheet%d", len(sheetNames)+1)
				}
				sheetNames = append(sheetNames, name)
			}
		case biffRecSST:
			chunks := [][]byte{rec.data}
			for i+1 < len(records) && records[i+1].id == biffRecContinue {
				i++
				chunks = append(chunks, records[i].data)
			}
			sst = parseBIFF8SST(chunks)
		}
	}

	var sheets []SpreadsheetSheet
	inWorksheet := false
	sheetIdx := 0
	var curCells map[int]map[int]string
	var pendingFormula bool
	var pendingRow, pendingCol int

	flushWorksheet := func() {
		if !inWorksheet {
			return
		}
		inWorksheet = false
		name := fmt.Sprintf("Sheet%d", sheetIdx+1)
		if sheetIdx < len(sheetNames) && sheetNames[sheetIdx] != "" {
			name = sheetNames[sheetIdx]
		}
		sheetIdx++
		if len(curCells) == 0 {
			return
		}
		rowIndices := make([]int, 0, len(curCells))
		for r := range curCells {
			rowIndices = append(rowIndices, r)
		}
		sort.Ints(rowIndices)

		var rows [][]string
		totalRows := 0
		truncated := false
		for _, rIdx := range rowIndices {
			colMap := curCells[rIdx]
			maxCol := -1
			for cIdx := range colMap {
				if cIdx <= 256 && cIdx > maxCol {
					maxCol = cIdx
				}
			}
			if maxCol < 0 {
				continue
			}
			cols := make([]string, maxCol+1)
			hasVal := false
			for cIdx, v := range colMap {
				if cIdx >= 0 && cIdx <= maxCol {
					cleaned := sanitizeCellText(v)
					if cleaned != "" {
						hasVal = true
					}
					cols[cIdx] = cleaned
				}
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
		if len(rows) > 0 {
			sheets = append(sheets, SpreadsheetSheet{
				Name:      name,
				Rows:      rows,
				TotalRows: totalRows,
				Truncated: truncated,
			})
		}
		curCells = nil
	}

	setCell := func(r, c int, val string) {
		if !inWorksheet || r < 0 || r > 100000 || c < 0 || c > 256 {
			return
		}
		if curCells == nil {
			curCells = make(map[int]map[int]string)
		}
		if curCells[r] == nil {
			curCells[r] = make(map[int]string)
		}
		curCells[r][c] = val
	}

	for _, rec := range records {
		switch rec.id {
		case biffRecBOF, biffRecBOF4, biffRecBOF2:
			flushWorksheet()
			if len(rec.data) >= 4 {
				dt := binary.LittleEndian.Uint16(rec.data[2:4])
				if dt == 0x0010 {
					inWorksheet = true
					curCells = make(map[int]map[int]string)
				}
			}
			pendingFormula = false
		case biffRecEOF:
			flushWorksheet()
			pendingFormula = false
		case biffRecLabelSST:
			pendingFormula = false
			if len(rec.data) >= 10 {
				r := int(binary.LittleEndian.Uint16(rec.data[0:2]))
				c := int(binary.LittleEndian.Uint16(rec.data[2:4]))
				idx := int(binary.LittleEndian.Uint32(rec.data[6:10]))
				if idx >= 0 && idx < len(sst) {
					setCell(r, c, sst[idx])
				}
			}
		case biffRecLabel, biffRecRString:
			pendingFormula = false
			if len(rec.data) >= 8 {
				r := int(binary.LittleEndian.Uint16(rec.data[0:2]))
				c := int(binary.LittleEndian.Uint16(rec.data[2:4]))
				setCell(r, c, decodeBIFF8LongString(rec.data[6:]))
			}
		case biffRecRK:
			pendingFormula = false
			if len(rec.data) >= 10 {
				r := int(binary.LittleEndian.Uint16(rec.data[0:2]))
				c := int(binary.LittleEndian.Uint16(rec.data[2:4]))
				rk := binary.LittleEndian.Uint32(rec.data[6:10])
				setCell(r, c, formatExcelFloat(decodeRKValue(rk)))
			}
		case biffRecMulRK:
			pendingFormula = false
			if len(rec.data) >= 6 {
				r := int(binary.LittleEndian.Uint16(rec.data[0:2]))
				firstCol := int(binary.LittleEndian.Uint16(rec.data[2:4]))
				pos := 4
				col := firstCol
				for pos+6 <= len(rec.data)-2 {
					rk := binary.LittleEndian.Uint32(rec.data[pos+2 : pos+6])
					setCell(r, col, formatExcelFloat(decodeRKValue(rk)))
					pos += 6
					col++
				}
			}
		case biffRecNumber:
			pendingFormula = false
			if len(rec.data) >= 14 {
				r := int(binary.LittleEndian.Uint16(rec.data[0:2]))
				c := int(binary.LittleEndian.Uint16(rec.data[2:4]))
				bits := binary.LittleEndian.Uint64(rec.data[6:14])
				setCell(r, c, formatExcelFloat(math.Float64frombits(bits)))
			}
		case biffRecBoolErr:
			pendingFormula = false
			if len(rec.data) >= 8 {
				r := int(binary.LittleEndian.Uint16(rec.data[0:2]))
				c := int(binary.LittleEndian.Uint16(rec.data[2:4]))
				if rec.data[7] == 0 {
					if rec.data[6] != 0 {
						setCell(r, c, "TRUE")
					} else {
						setCell(r, c, "FALSE")
					}
				}
			}
		case biffRecFormula:
			pendingFormula = false
			if len(rec.data) >= 14 {
				r := int(binary.LittleEndian.Uint16(rec.data[0:2]))
				c := int(binary.LittleEndian.Uint16(rec.data[2:4]))
				res := rec.data[6:14]
				if res[6] == 0xFF && res[7] == 0xFF {
					switch res[0] {
					case 0:
						pendingFormula = true
						pendingRow = r
						pendingCol = c
					case 1:
						if res[2] != 0 {
							setCell(r, c, "TRUE")
						} else {
							setCell(r, c, "FALSE")
						}
					}
				} else {
					bits := binary.LittleEndian.Uint64(res)
					setCell(r, c, formatExcelFloat(math.Float64frombits(bits)))
				}
			}
		case biffRecString:
			if pendingFormula && len(rec.data) >= 3 {
				setCell(pendingRow, pendingCol, decodeBIFF8LongString(rec.data))
			}
			pendingFormula = false
		}
	}
	flushWorksheet()
	return sheets, nil
}

func decodeRKValue(rk uint32) float64 {
	var val float64
	if rk&0x02 != 0 {
		val = float64(int32(rk) >> 2)
	} else {
		bits := uint64(rk&^uint32(3)) << 32
		val = math.Float64frombits(bits)
	}
	if rk&0x01 != 0 {
		val /= 100.0
	}
	return val
}

func formatExcelFloat(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func decodeBIFF8ShortString(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	charCount := int(b[0])
	flags := b[1]
	pos := 2
	if flags&0x01 != 0 {
		var u16s []uint16
		for i := 0; i < charCount && pos+2 <= len(b); i++ {
			u16s = append(u16s, binary.LittleEndian.Uint16(b[pos:pos+2]))
			pos += 2
		}
		return string(utf16.Decode(u16s))
	}
	end := minInt(pos+charCount, len(b))
	runes := make([]rune, 0, end-pos)
	for _, c := range b[pos:end] {
		runes = append(runes, rune(c))
	}
	return string(runes)
}

func decodeBIFF8LongString(b []byte) string {
	if len(b) < 3 {
		return ""
	}
	charCount := int(binary.LittleEndian.Uint16(b[0:2]))
	flags := b[2]
	pos := 3
	if flags&0x08 != 0 && pos+2 <= len(b) {
		pos += 2
	}
	if flags&0x04 != 0 && pos+4 <= len(b) {
		pos += 4
	}
	if flags&0x01 != 0 {
		var u16s []uint16
		for i := 0; i < charCount && pos+2 <= len(b); i++ {
			u16s = append(u16s, binary.LittleEndian.Uint16(b[pos:pos+2]))
			pos += 2
		}
		return string(utf16.Decode(u16s))
	}
	end := minInt(pos+charCount, len(b))
	runes := make([]rune, 0, end-pos)
	for _, c := range b[pos:end] {
		runes = append(runes, rune(c))
	}
	return string(runes)
}

func parseBIFF8SST(chunks [][]byte) []string {
	if len(chunks) == 0 || len(chunks[0]) < 8 {
		return nil
	}
	uniqueCount := int(binary.LittleEndian.Uint32(chunks[0][4:8]))
	if uniqueCount <= 0 {
		return nil
	}
	if uniqueCount > 200000 {
		uniqueCount = 200000
	}

	cIdx := 0
	pos := 8

	advanceChunk := func() bool {
		for cIdx < len(chunks) && pos >= len(chunks[cIdx]) {
			cIdx++
			pos = 0
		}
		return cIdx < len(chunks)
	}
	readByte := func() (byte, bool) {
		if !advanceChunk() {
			return 0, false
		}
		b := chunks[cIdx][pos]
		pos++
		return b, true
	}
	readUint16 := func() (uint16, bool) {
		b0, ok0 := readByte()
		b1, ok1 := readByte()
		if !ok0 || !ok1 {
			return 0, false
		}
		return uint16(b0) | (uint16(b1) << 8), true
	}
	readUint32 := func() (uint32, bool) {
		w0, ok0 := readUint16()
		w1, ok1 := readUint16()
		if !ok0 || !ok1 {
			return 0, false
		}
		return uint32(w0) | (uint32(w1) << 16), true
	}
	skipBytes := func(n int) {
		for n > 0 && advanceChunk() {
			avail := len(chunks[cIdx]) - pos
			if avail <= n {
				n -= avail
				cIdx++
				pos = 0
			} else {
				pos += n
				n = 0
			}
		}
	}

	result := make([]string, 0, minInt(uniqueCount, 4096))
	for s := 0; s < uniqueCount; s++ {
		charCountU16, ok := readUint16()
		if !ok {
			break
		}
		flags, ok := readByte()
		if !ok {
			break
		}
		highByte := (flags & 0x01) != 0
		var richRuns int
		if flags&0x08 != 0 {
			r, ok := readUint16()
			if !ok {
				break
			}
			richRuns = int(r)
		}
		var extBytes int
		if flags&0x04 != 0 {
			e, ok := readUint32()
			if !ok {
				break
			}
			extBytes = int(e)
		}

		remaining := int(charCountU16)
		var sb strings.Builder
		for remaining > 0 {
			if cIdx < len(chunks) && pos >= len(chunks[cIdx]) {
				cIdx++
				pos = 0
				if cIdx >= len(chunks) || len(chunks[cIdx]) == 0 {
					break
				}
				contFlag := chunks[cIdx][0]
				pos = 1
				highByte = (contFlag & 0x01) != 0
			}
			if !advanceChunk() {
				break
			}
			if highByte {
				b0, ok0 := readByte()
				b1, ok1 := readByte()
				if !ok0 || !ok1 {
					break
				}
				u := uint16(b0) | (uint16(b1) << 8)
				sb.WriteRune(rune(u))
			} else {
				b0, ok0 := readByte()
				if !ok0 {
					break
				}
				sb.WriteRune(rune(b0))
			}
			remaining--
		}
		skipBytes(richRuns * 4)
		skipBytes(extBytes)
		result = append(result, sb.String())
	}
	return result
}

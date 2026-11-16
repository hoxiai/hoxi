package attachment

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

type pptxSlideSpec struct {
	Title string
	Lines []string
}

func parseMarkdownSlides(defaultTitle, markdown string) []pptxSlideSpec {
	text := strings.ReplaceAll(strings.ReplaceAll(markdown, "\r\n", "\n"), "\r", "\n")
	rawLines := strings.Split(text, "\n")

	var slides []pptxSlideSpec
	var cur pptxSlideSpec
	flush := func() {
		if strings.TrimSpace(cur.Title) != "" || len(cur.Lines) > 0 {
			if strings.TrimSpace(cur.Title) == "" {
				if len(slides) == 0 && strings.TrimSpace(defaultTitle) != "" {
					cur.Title = strings.TrimSpace(defaultTitle)
				} else {
					cur.Title = fmt.Sprintf("Slide %d", len(slides)+1)
				}
			}
			slides = append(slides, cur)
			cur = pptxSlideSpec{}
		}
	}

	for _, raw := range rawLines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "---" || trimmed == "***" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if strings.TrimSpace(cur.Title) != "" || len(cur.Lines) > 0 {
				flush()
			}
			cur.Title = heading
			continue
		}
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			inner := strings.Trim(trimmed, "|")
			cells := strings.Split(inner, "|")
			isSep := true
			var rowCells []string
			for _, c := range cells {
				clean := strings.TrimSpace(c)
				rowCells = append(rowCells, clean)
				if clean == "" || strings.Trim(clean, "-: ") != "" {
					isSep = false
				}
			}
			if isSep {
				continue
			}
			cur.Lines = append(cur.Lines, strings.Join(rowCells, "  |  "))
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			cur.Lines = append(cur.Lines, "• "+strings.TrimSpace(trimmed[2:]))
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			cur.Lines = append(cur.Lines, strings.TrimSpace(trimmed[4:]))
			continue
		}
		cur.Lines = append(cur.Lines, trimmed)
	}
	flush()

	if len(slides) == 0 {
		t := strings.TrimSpace(defaultTitle)
		if t == "" {
			t = "Presentation"
		}
		slides = append(slides, pptxSlideSpec{Title: t})
	} else if strings.TrimSpace(defaultTitle) != "" && slides[0].Title == "Slide 1" {
		slides[0].Title = strings.TrimSpace(defaultTitle)
	}
	return slides
}

// WritePPTXFile writes a standard OpenXML .pptx presentation from Markdown text.
// Slides are separated by `#` / `##` headings or `---` rules.
func WritePPTXFile(filePath, title, markdownText string) error {
	slides := parseMarkdownSlides(title, markdownText)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	addZipFile := func(name, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}

	var ctOverrides strings.Builder
	for i := range slides {
		fmt.Fprintf(&ctOverrides, `<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i+1)
	}

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>` +
		`<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>` +
		`<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>` +
		`<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>` +
		ctOverrides.String() +
		`</Types>`
	if err := addZipFile("[Content_Types].xml", contentTypes); err != nil {
		return err
	}

	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>` +
		`</Relationships>`
	if err := addZipFile("_rels/.rels", rootRels); err != nil {
		return err
	}

	var sldIdLst strings.Builder
	var presRels strings.Builder
	for i := range slides {
		rId := i + 3
		sldID := 256 + i
		fmt.Fprintf(&sldIdLst, `<p:sldId id="%d" r:id="rId%d"/>`, sldID, rId)
		fmt.Fprintf(&presRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, rId, i+1)
	}

	presXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
		`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>` +
		`<p:sldIdLst>` + sldIdLst.String() + `</p:sldIdLst>` +
		`<p:sldSz cx="12192000" cy="6858000"/>` +
		`<p:notesSz cx="6858000" cy="9144000"/>` +
		`</p:presentation>`
	if err := addZipFile("ppt/presentation.xml", presXML); err != nil {
		return err
	}

	presRelsXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/>` +
		presRels.String() +
		`</Relationships>`
	if err := addZipFile("ppt/_rels/presentation.xml.rels", presRelsXML); err != nil {
		return err
	}

	slideMasterXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
		`<p:cSld><p:spTree>` +
		`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
		`</p:spTree></p:cSld>` +
		`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
		`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>` +
		`</p:sldMaster>`
	if err := addZipFile("ppt/slideMasters/slideMaster1.xml", slideMasterXML); err != nil {
		return err
	}

	slideMasterRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>` +
		`</Relationships>`
	if err := addZipFile("ppt/slideMasters/_rels/slideMaster1.xml.rels", slideMasterRels); err != nil {
		return err
	}

	slideLayoutXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1">` +
		`<p:cSld name="Blank"><p:spTree>` +
		`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
		`</p:spTree></p:cSld>` +
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>` +
		`</p:sldLayout>`
	if err := addZipFile("ppt/slideLayouts/slideLayout1.xml", slideLayoutXML); err != nil {
		return err
	}

	slideLayoutRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>` +
		`</Relationships>`
	if err := addZipFile("ppt/slideLayouts/_rels/slideLayout1.xml.rels", slideLayoutRels); err != nil {
		return err
	}

	themeXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office Theme">` +
		`<a:themeElements>` +
		`<a:clrScheme name="Office">` +
		`<a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1>` +
		`<a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
		`<a:dk2><a:srgbClr val="1F497D"/></a:dk2>` +
		`<a:lt2><a:srgbClr val="EEECE1"/></a:lt2>` +
		`<a:accent1><a:srgbClr val="4F81BD"/></a:accent1>` +
		`<a:accent2><a:srgbClr val="C0504D"/></a:accent2>` +
		`<a:accent3><a:srgbClr val="9BBB59"/></a:accent3>` +
		`<a:accent4><a:srgbClr val="8064A2"/></a:accent4>` +
		`<a:accent5><a:srgbClr val="4BACC6"/></a:accent5>` +
		`<a:accent6><a:srgbClr val="F79646"/></a:accent6>` +
		`<a:hlink><a:srgbClr val="0000FF"/></a:hlink>` +
		`<a:folHlink><a:srgbClr val="800080"/></a:folHlink>` +
		`</a:clrScheme>` +
		`<a:fontScheme name="Office">` +
		`<a:majorFont><a:latin typeface="Calibri"/><a:ea typeface="PingFang SC"/><a:cs typeface=""/></a:majorFont>` +
		`<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface="PingFang SC"/><a:cs typeface=""/></a:minorFont>` +
		`</a:fontScheme>` +
		`<a:fmtScheme name="Office"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme>` +
		`</a:themeElements>` +
		`</a:theme>`
	if err := addZipFile("ppt/theme/theme1.xml", themeXML); err != nil {
		return err
	}

	slideRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>` +
		`</Relationships>`

	for i, slide := range slides {
		var bodyParas strings.Builder
		if len(slide.Lines) == 0 {
			bodyParas.WriteString(`<a:p><a:endParaRPr lang="en-US" sz="1800"/></a:p>`)
		} else {
			for _, line := range slide.Lines {
				fmt.Fprintf(&bodyParas,
					`<a:p><a:r><a:rPr lang="en-US" sz="1800"/><a:t>%s</a:t></a:r></a:p>`,
					xmlEscapeString(line),
				)
			}
		}

		slideXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
			`<p:cSld><p:spTree>` +
			`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
			`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
			`<p:sp>` +
			`<p:nvSpPr><p:cNvPr id="2" name="Title"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>` +
			`<p:spPr><a:xfrm><a:off x="685800" y="457200"/><a:ext cx="10820400" cy="914400"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>` +
			`<p:txBody><a:bodyPr wrap="square"/><a:lstStyle/>` +
			`<a:p><a:r><a:rPr lang="en-US" sz="2800" b="1"/><a:t>` + xmlEscapeString(slide.Title) + `</a:t></a:r></a:p>` +
			`</p:txBody>` +
			`</p:sp>` +
			`<p:sp>` +
			`<p:nvSpPr><p:cNvPr id="3" name="Content"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>` +
			`<p:spPr><a:xfrm><a:off x="685800" y="1524000"/><a:ext cx="10820400" cy="4800600"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>` +
			`<p:txBody><a:bodyPr wrap="square"/><a:lstStyle/>` +
			bodyParas.String() +
			`</p:txBody>` +
			`</p:sp>` +
			`</p:spTree></p:cSld>` +
			`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>` +
			`</p:sld>`

		if err := addZipFile(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), slideXML); err != nil {
			return err
		}
		if err := addZipFile(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), slideRels); err != nil {
			return err
		}
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return writeFileAtomic(filePath, buf.Bytes())
}

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Spreadsheet data: CSV, TSV, JSON and Excel .xlsx (first sheet), each to any of the others.

type dataTable struct {
	Header []string   `json:"header"`
	Rows   [][]string `json:"rows"`
}

func readTable(in string) (dataTable, error) {
	switch extOf(in) {
	case "xlsx":
		return readXlsx(in)
	}
	b, err := os.ReadFile(in)
	if err != nil {
		return dataTable{}, cvError("Couldn't read the file.")
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	switch extOf(in) {
	case "json":
		return readJSONTable(s)
	case "tsv":
		return readCSV(s, '\t'), nil
	}
	return readCSV(s, 0), nil
}

func dataConvert(in, out string) error {
	inExt, outExt := extOf(in), extOf(out)
	if inExt == outExt {
		return cvError("The file is already " + strings.ToUpper(inExt) + ". Pick another format.")
	}
	t, err := readTable(in)
	if err != nil {
		return err
	}
	if len(t.Header) == 0 && len(t.Rows) == 0 {
		return cvError("No data found in this file.")
	}
	switch outExt {
	case "csv": // the mark makes Excel read non-English letters right
		return os.WriteFile(out, []byte("\ufeff"+writeDelimited(t, ',')), 0o644)
	case "tsv":
		return os.WriteFile(out, []byte(writeDelimited(t, '\t')), 0o644)
	case "json":
		return os.WriteFile(out, []byte(writeJSONTable(t)), 0o644)
	case "xlsx":
		return writeXlsx(t, out)
	}
	return cvError("Spreadsheets can't be saved as " + outExt + ".")
}

func readCSV(text string, d rune) dataTable {
	if d == 0 {
		first := strings.SplitN(text, "\n", 2)[0]
		best, n := ',', -1
		for _, c := range []rune{',', ';', '\t', '|'} {
			if k := strings.Count(first, string(c)); k > n {
				best, n = c, k
			}
		}
		d = best
	}
	var rows [][]string
	var row []string
	var cell strings.Builder
	quoted := false
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if quoted {
			if c == '"' {
				if i+1 < len(rs) && rs[i+1] == '"' {
					cell.WriteRune('"')
					i++
				} else {
					quoted = false
				}
			} else {
				cell.WriteRune(c)
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case d:
			row = append(row, cell.String())
			cell.Reset()
		case '\r':
		case '\n':
			row = append(row, cell.String())
			cell.Reset()
			rows = append(rows, row)
			row = nil
		default:
			cell.WriteRune(c)
		}
	}
	if cell.Len() > 0 || len(row) > 0 {
		rows = append(rows, append(row, cell.String()))
	}
	var clean [][]string
	for _, r := range rows {
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				clean = append(clean, r)
				break
			}
		}
	}
	if len(clean) == 0 {
		return dataTable{}
	}
	return dataTable{clean[0], clean[1:]}
}

func writeDelimited(t dataTable, d rune) string {
	q := func(s string) string {
		if strings.ContainsAny(s, string(d)+"\"\n\r") {
			return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
		}
		return s
	}
	var b strings.Builder
	for _, r := range append([][]string{t.Header}, t.Rows...) {
		for i, c := range r {
			if i > 0 {
				b.WriteRune(d)
			}
			b.WriteString(q(c))
		}
		b.WriteString("\r\n")
	}
	return b.String()
}

func jsonStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func readJSONTable(text string) (dataTable, error) {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return dataTable{}, cvError("This JSON file is damaged: " + err.Error())
	}
	if o, ok := v.(map[string]any); ok { // {"items": [...]} -> the first list inside
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		found := false
		for _, k := range keys {
			if a, ok := o[k].([]any); ok {
				v, found = a, true
				break
			}
		}
		if !found {
			v = []any{o}
		}
	}
	arr, ok := v.([]any)
	if !ok {
		return dataTable{}, cvError("This JSON isn't a list of rows.")
	}
	allArrays := len(arr) > 0
	for _, x := range arr {
		if _, ok := x.([]any); !ok {
			allArrays = false
		}
	}
	if allArrays {
		var rows [][]string
		for _, x := range arr {
			var r []string
			for _, c := range x.([]any) {
				r = append(r, jsonStr(c))
			}
			rows = append(rows, r)
		}
		return dataTable{rows[0], rows[1:]}, nil
	}
	// Keys in the order they first appear.
	var header []string
	seen := map[string]bool{}
	for _, x := range arr {
		if o, ok := x.(map[string]any); ok {
			for _, k := range orderedKeys(text, o) {
				if !seen[k] {
					seen[k] = true
					header = append(header, k)
				}
			}
		}
	}
	if len(header) == 0 {
		t := dataTable{Header: []string{"value"}}
		for _, x := range arr {
			t.Rows = append(t.Rows, []string{jsonStr(x)})
		}
		return t, nil
	}
	t := dataTable{Header: header}
	for _, x := range arr {
		o, _ := x.(map[string]any)
		r := make([]string, len(header))
		for i, k := range header {
			r[i] = jsonStr(o[k])
		}
		t.Rows = append(t.Rows, r)
	}
	return t, nil
}

// orderedKeys returns o's keys in the order they appear in the text (Go maps forget the order).
func orderedKeys(text string, o map[string]any) []string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	pos := func(k string) int {
		b, _ := json.Marshal(k)
		if i := strings.Index(text, string(b)); i >= 0 {
			return i
		}
		return math.MaxInt32
	}
	sort.SliceStable(keys, func(a, b int) bool { return pos(keys[a]) < pos(keys[b]) })
	return keys
}

var numberRe = regexp.MustCompile(`^-?(0|[1-9]\d{0,14})(\.\d+)?$`)

func writeJSONTable(t dataTable) string {
	var b strings.Builder
	b.WriteString("[\n")
	for ri, r := range t.Rows {
		b.WriteString("  {")
		for i, k := range t.Header {
			if k == "" {
				k = fmt.Sprintf("column %d", i+1)
			}
			v := ""
			if i < len(r) {
				v = r[i]
			}
			kb, _ := json.Marshal(k)
			if i > 0 {
				b.WriteString(", ")
			}
			b.Write(kb)
			b.WriteString(": ")
			if numberRe.MatchString(v) {
				b.WriteString(v)
			} else {
				vb, _ := json.Marshal(v)
				b.Write(vb)
			}
		}
		b.WriteString("}")
		if ri < len(t.Rows)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	return b.String()
}

func colIndex(ref string) int {
	n := 0
	for _, c := range strings.ToUpper(ref) {
		if c < 'A' || c > 'Z' {
			break
		}
		n = n*26 + int(c-'A'+1)
	}
	return n - 1
}

func colName(i int) string {
	n := i + 1
	s := ""
	for n > 0 {
		s = string(rune('A'+(n-1)%26)) + s
		n = (n - 1) / 26
	}
	return s
}

func readXlsx(in string) (dataTable, error) {
	z, err := zip.OpenReader(in)
	if err != nil {
		return dataTable{}, cvError("This doesn't look like an Excel file.")
	}
	defer z.Close()
	var shared []string
	if b, ok := zipRead(&z.Reader, "xl/sharedStrings.xml"); ok {
		d := xml.NewDecoder(bytes.NewReader(b))
		var cur *strings.Builder
		inT := false
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "si":
					cur = &strings.Builder{}
				case "t":
					inT = true
				}
			case xml.CharData:
				if inT && cur != nil {
					cur.Write(t)
				}
			case xml.EndElement:
				switch t.Name.Local {
				case "t":
					inT = false
				case "si":
					if cur != nil {
						shared = append(shared, cur.String())
					}
					cur = nil
				}
			}
		}
	}
	sheet := "xl/worksheets/sheet1.xml"
	if _, ok := zipRead(&z.Reader, sheet); !ok {
		sheet = ""
		for _, f := range z.File {
			if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") && (sheet == "" || f.Name < sheet) {
				sheet = f.Name
			}
		}
		if sheet == "" {
			return dataTable{}, cvError("This doesn't look like an Excel file.")
		}
	}
	data, _ := zipRead(&z.Reader, sheet)
	d := xml.NewDecoder(bytes.NewReader(data))
	var rows [][]string
	var row map[int]string
	col, nextCol := 0, 0
	typ := ""
	var val *strings.Builder
	inV := false
	for {
		tok, err := d.Token()
		if err == io.EOF || err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row, nextCol = map[int]string{}, 0
			case "c":
				col = nextCol
				if r, ok := attr(t, "r"); ok {
					col = colIndex(r)
				}
				typ, _ = attr(t, "t")
				val = &strings.Builder{}
			case "v", "t":
				inV = true
			}
		case xml.CharData:
			if inV && val != nil {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inV = false
			case "c":
				raw := ""
				if val != nil {
					raw = val.String()
				}
				v := raw
				switch typ {
				case "s":
					if i, err := strconv.Atoi(raw); err == nil && i >= 0 && i < len(shared) {
						v = shared[i]
					} else {
						v = ""
					}
				case "b":
					v = map[bool]string{true: "TRUE", false: "FALSE"}[raw == "1"]
				case "inlineStr", "str":
				default:
					if f, err := strconv.ParseFloat(raw, 64); err == nil && f == math.Floor(f) && math.Abs(f) < 1e15 {
						v = strconv.FormatInt(int64(f), 10)
					}
				}
				if row != nil && col >= 0 && col < 16384 {
					row[col] = v
				}
				nextCol = col + 1
			case "row":
				if len(row) > 0 {
					w := 0
					for k := range row {
						w = max(w, k+1)
					}
					r := make([]string, w)
					for k, v := range row {
						r[k] = v
					}
					rows = append(rows, r)
				}
			}
		}
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	for i := range rows {
		for len(rows[i]) < width {
			rows[i] = append(rows[i], "")
		}
	}
	if len(rows) == 0 {
		return dataTable{}, nil
	}
	return dataTable{rows[0], rows[1:]}, nil
}

func xmlText(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= ' ' {
			return r
		}
		return -1
	}, s)))
	return b.String()
}

func writeXlsx(t dataTable, out string) error {
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for ri, r := range append([][]string{t.Header}, t.Rows...) {
		fmt.Fprintf(&sheet, `<row r="%d">`, ri+1)
		for ci, v := range r {
			ref := fmt.Sprintf("%s%d", colName(ci), ri+1)
			switch {
			case ri > 0 && numberRe.MatchString(v):
				fmt.Fprintf(&sheet, `<c r="%s"><v>%s</v></c>`, ref, v)
			case v != "":
				s := ""
				if ri == 0 {
					s = ` s="1"`
				}
				fmt.Fprintf(&sheet, `<c r="%s" t="inlineStr"%s><is><t xml:space="preserve">%s</t></is></c>`, ref, s, xmlText(v))
			}
		}
		sheet.WriteString("</row>")
	}
	sheet.WriteString("</sheetData></worksheet>")
	files := [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`},
		{"xl/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>
<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>
<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>
</styleSheet>`},
		{"xl/worksheets/sheet1.xml", sheet.String()},
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	z := zip.NewWriter(f)
	for _, fl := range files {
		w, _ := z.Create(fl[0])
		io.WriteString(w, fl[1])
	}
	return z.Close()
}

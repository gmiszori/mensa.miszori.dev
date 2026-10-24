package food2050

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Next.js pages carry their data to the browser in a format called the React Server
// Components (RSC) payload. It arrives in <script> tags as text chunks,
//
//	self.__next_f.push([1,"..."])
//
// and the concatenated text is a list of numbered rows, one per line:
//
//	14:{"outlet":{"menuCategory":{...}}}
//	15:I["chunk.js","default"]
//
// To keep the page small, an object that appears twice is written only once; later copies
// are replaced by a reference string such as "$14:props:outlet:menuCategory:...:allergen",
// meaning "row 14, then follow these keys". nextData reads the rows and follows those
// references, so callers always get complete objects.

type nextData struct {
	rows  map[string]any
	order []string // row ids in page order, so results come out in a stable order
}

// chunkPattern matches one data chunk: self.__next_f.push([1,"..."])
var chunkPattern = regexp.MustCompile(`(?s)self\.__next_f\.push\((\[.*?\])\)</script>`)

// parseNextData extracts and parses the RSC rows embedded in an HTML page.
func parseNextData(html []byte) *nextData {
	var b strings.Builder
	for _, m := range chunkPattern.FindAllSubmatch(html, -1) {
		var chunk []any
		if json.Unmarshal(m[1], &chunk) != nil || len(chunk) < 2 {
			continue
		}
		if s, ok := chunk[1].(string); ok {
			b.WriteString(s)
		}
	}
	return parseRows(b.String())
}

// parseRows splits the payload into rows. A row is "<hex id>:<value>". Most values are JSON,
// sometimes after a one- or two-letter tag ("I[...]" for code modules). "T<hex length>,"
// starts a text row of that many bytes, which we skip.
func parseRows(text string) *nextData {
	d := &nextData{rows: map[string]any{}}
	for i := 0; i < len(text); {
		colon := strings.IndexByte(text[i:], ':')
		if colon < 0 {
			break
		}
		id := text[i : i+colon]
		i += colon + 1
		if !isHex(id) { // e.g. ":HL[...]", a resource hint without an id
			i = nextLine(text, i)
			continue
		}
		// Text row: skip exactly the announced number of bytes.
		if strings.HasPrefix(text[i:], "T") {
			if comma := strings.IndexByte(text[i:], ','); comma > 1 && isHex(text[i+1:i+comma]) {
				n, _ := strconv.ParseInt(text[i+1:i+comma], 16, 64)
				i = min(len(text), i+comma+1+int(n))
				continue
			}
		}
		// Skip a type tag such as "I" or "HL" in front of the JSON. Some rows are only a tag
		// ("5:X"); they carry nothing we need.
		for i < len(text) && text[i] >= 'A' && text[i] <= 'Z' {
			i++
		}
		if i >= len(text) || text[i] == '\n' {
			i = nextLine(text, i)
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var v any
		if err := dec.Decode(&v); err != nil {
			i = nextLine(text, i)
			continue
		}
		d.rows[id] = v
		d.order = append(d.order, id)
		i += int(dec.InputOffset())
		if i < len(text) && text[i] == '\n' {
			i++
		}
	}
	return d
}

// find returns, as JSON, every object whose "__typename" is typename, with all references
// inside it replaced by the objects they point to.
func (d *nextData) find(typename string) []json.RawMessage {
	var out []json.RawMessage
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["__typename"] == typename {
				if raw, err := json.Marshal(d.resolve(v, 0)); err == nil {
					out = append(out, raw)
				}
				return
			}
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(v[k])
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	for _, id := range d.order {
		walk(d.rows[id])
	}
	return out
}

// resolve returns a copy of v with every reference replaced by its target. depth stops
// endless loops in case references ever point at each other.
func (d *nextData) resolve(v any, depth int) any {
	if depth > 40 {
		return nil
	}
	switch v := v.(type) {
	case string:
		if isRef(v) {
			return d.resolve(d.lookup(v, depth), depth+1)
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = d.resolve(x, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = d.resolve(x, depth+1)
		}
		return out
	default:
		return v
	}
}

// lookup follows a reference such as "$14:props:outlet:menuItems:0:dish".
func (d *nextData) lookup(ref string, depth int) any {
	parts := strings.Split(ref[1:], ":")
	v := d.rows[parts[0]]
	for _, key := range parts[1:] {
		if s, ok := v.(string); ok && isRef(s) && depth < 40 {
			v = d.lookup(s, depth+1)
		}
		switch x := v.(type) {
		case map[string]any:
			v = x[key]
		case []any:
			// A React element is written as ["$", type, key, props]; "props" means its 4th entry.
			if key == "props" && len(x) == 4 && x[0] == "$" {
				v = x[3]
				continue
			}
			n, err := strconv.Atoi(key)
			if err != nil || n < 0 || n >= len(x) {
				return nil
			}
			v = x[n]
		default:
			return nil
		}
	}
	return v
}

// isRef reports whether s is a reference to a row: "$" followed by a hex row id. Other
// strings starting with "$" ("$undefined", "$L1b", "$$text") are something else.
func isRef(s string) bool {
	if len(s) < 2 || s[0] != '$' {
		return false
	}
	id, _, _ := strings.Cut(s[1:], ":")
	return isHex(id)
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func nextLine(text string, i int) int {
	if nl := strings.IndexByte(text[i:], '\n'); nl >= 0 {
		return i + nl + 1
	}
	return len(text)
}

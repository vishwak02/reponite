// snake.go renders a query result struct as the wire envelope's stable
// lowercase snake_case keys, preserving field order and dropping empty values
// (null, "", [], {}, false — except `truncated`, which an agent must always
// see). It spares the map/explain result trees a parallel DTO per nested
// type while keeping the same wire conventions as output.go: `Meta` becomes
// `_meta`, and the output is compact under CompactOutput.
package interfaces

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
)

// orderedObj is a JSON object with its key order kept.
type orderedObj struct {
	keys []string
	vals []interface{}
}

func (o orderedObj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(o.vals[i])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// snakeKey maps a Go field name to the wire key: StartLine → start_line,
// ResolutionMethod → resolution_method, Meta → _meta.
func snakeKey(k string) string {
	if k == "Meta" {
		return "_meta"
	}
	var b strings.Builder
	rs := []rune(k)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// snakeJSON renders v with snake_case keys and empty values dropped.
func snakeJSON(v interface{}) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tree, err := decodeOrdered(dec, false)
	if err != nil {
		return "", err
	}
	return marshal(prune(tree, ""))
}

// dataKeys are maps whose keys are data (repo names, kinds), never renamed.
var dataKeys = map[string]bool{"Refs": true, "Omitted": true, "EntryPoints": true}

func decodeOrdered(dec *json.Decoder, keepKeys bool) (interface{}, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := orderedObj{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				val, err := decodeOrdered(dec, dataKeys[k])
				if err != nil {
					return nil, err
				}
				if !keepKeys {
					k = snakeKey(k)
				}
				o.keys = append(o.keys, k)
				o.vals = append(o.vals, val)
			}
			_, err := dec.Token() // }
			return o, err
		case '[':
			arr := []interface{}{}
			for dec.More() {
				val, err := decodeOrdered(dec, false)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			_, err := dec.Token() // ]
			return arr, err
		}
	}
	return tok, nil
}

// prune drops empty values from objects (arrays keep their elements).
func prune(v interface{}, key string) interface{} {
	switch t := v.(type) {
	case orderedObj:
		out := orderedObj{}
		for i, k := range t.keys {
			pv := prune(t.vals[i], k)
			if isEmpty(pv) && k != "truncated" {
				continue
			}
			out.keys = append(out.keys, k)
			out.vals = append(out.vals, pv)
		}
		return out
	case []interface{}:
		for i := range t {
			t[i] = prune(t[i], "")
		}
		return t
	}
	return v
}

func isEmpty(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case []interface{}:
		return len(t) == 0
	case orderedObj:
		return len(t.keys) == 0
	}
	return false
}

package config

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type span struct{ start, end int }
type doc struct {
	values     map[string]span
	tables     map[string]int
	firstTable int
	body       []byte
}

func parseTOML(b []byte) (doc, error) {
	var check map[string]any
	if e := toml.Unmarshal(b, &check); e != nil {
		return doc{}, fmt.Errorf("invalid TOML: %w", e)
	}
	d := doc{values: map[string]span{}, tables: map[string]int{}, firstTable: len(b), body: b}
	var p unstable.Parser
	p.Reset(b)
	table := ""
	for p.NextExpression() {
		n := p.Expression()
		keys := []string{}
		it := n.Key()
		for it.Next() {
			keys = append(keys, string(it.Node().Data))
		}
		key := strings.Join(keys, ".")
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			start := 0
			it := n.Key()
			if it.Next() {
				start = int(it.Node().Raw.Offset)
				for start > 0 && b[start-1] != '\n' {
					start--
				}
			}
			if d.firstTable == len(b) {
				d.firstTable = start
			}
			if table != "" {
				d.tables[table] = start
			}
			table = key
			d.tables[table] = len(b)
		case unstable.KeyValue:
			it := n.Key()
			if !it.Next() {
				continue
			}
			start := int(it.Node().Raw.Offset)
			v := n.Value()
			end := int(v.Raw.Offset + v.Raw.Length)
			if v.Kind == unstable.Array || v.Kind == unstable.InlineTable {
				end = complexValueEnd(b, int(v.Raw.Offset), v.Kind)
			}
			full := key
			if table != "" {
				full = table + "." + key
			}
			d.values[full] = span{start, end}
		}
	}
	return d, p.Error()
}

// Raw spans for array/inline-table nodes cover their opening delimiter only.
// Scan balanced delimiters, respecting strings and comments, to include closing
// punctuation without swallowing the inline comment or the next expression.
func complexValueEnd(b []byte, start int, _ unstable.Kind) int {
	depth := 0
	for i := start; i < len(b); i++ {
		c := b[i]
		if c == '#' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote := c
			triple := i+2 < len(b) && b[i+1] == c && b[i+2] == c
			if triple {
				i += 2
			}
			for i++; i < len(b); i++ {
				if quote == '"' && b[i] == '\\' {
					i++
					continue
				}
				if b[i] == quote {
					if !triple {
						break
					}
					if i+2 < len(b) && b[i+1] == quote && b[i+2] == quote {
						i += 2
						break
					}
				}
			}
			continue
		}
		if c == '[' || c == '{' {
			depth++
		}
		if c == ']' || c == '}' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(b)
}
func tomlValue(v any) (string, error) {
	b, e := toml.Marshal(map[string]any{"value": v})
	if e != nil {
		return "", e
	}
	_, after, ok := strings.Cut(string(b), "=")
	if !ok {
		return "", fmt.Errorf("cannot encode TOML value")
	}
	return strings.TrimSpace(after), nil
}

// PatchTOML replaces only owned expressions, preserving all other source bytes.
// A nil map value removes an expression. New top-level keys precede any table.
func PatchTOML(b []byte, changes map[string]any) ([]byte, error) {
	keys := []string{}
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		d, e := parseTOML(b)
		if e != nil {
			return nil, e
		}
		v := changes[key]
		expr, exists := d.values[key]
		if exists {
			if v == nil {
				start, end := expr.start, expr.end
				for start > 0 && b[start-1] != '\n' {
					start--
				}
				for end < len(b) && b[end] != '\n' {
					end++
				}
				if end < len(b) {
					end++
				}
				b = append(append([]byte(nil), b[:start]...), b[end:]...)
				continue
			}
			value, e := tomlValue(v)
			if e != nil {
				return nil, e
			}
			raw := b[expr.start:expr.end]
			eq := bytes.IndexByte(raw, '=')
			if eq < 0 {
				return nil, fmt.Errorf("unsupported TOML expression %s", key)
			}
			replacement := append(append([]byte(nil), raw[:eq+1]...), []byte(" "+value)...)
			b = append(append(append([]byte(nil), b[:expr.start]...), replacement...), b[expr.end:]...)
		} else if v != nil {
			value, e := tomlValue(v)
			if e != nil {
				return nil, e
			}
			table, name := "", key
			hasTable := strings.Contains(key, ".")
			if hasTable {
				idx := strings.LastIndex(key, ".")
				table, name = key[:idx], key[idx+1:]
			}
			if !hasTable {
				line := []byte(name + " = " + value + "\n")
				if d.firstTable > 0 && b[d.firstTable-1] != '\n' {
					line = append([]byte("\n"), line...)
				}
				b = append(append(append([]byte(nil), b[:d.firstTable]...), line...), b[d.firstTable:]...)
			} else if pos, ok := d.tables[table]; ok {
				line := []byte("\n" + name + " = " + value + "\n")
				b = append(append(append([]byte(nil), b[:pos]...), line...), b[pos:]...)
			} else {
				b = append(b, []byte("\n["+table+"]\n"+name+" = "+value+"\n")...)
			}
		}
	}
	_, e := parseTOML(b)
	return b, e
}
func readTOML(b []byte) (map[string]any, error) {
	var m map[string]any
	e := toml.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m, e
}

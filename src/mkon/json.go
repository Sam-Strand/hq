package mkon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"unicode/utf8"
)

// FromJSON разбирает один JSON-документ в модель mkon, сохраняя порядок ключей.
// Числа — float64 (как в JS); дубликаты ключей перезаписывают значение, сохраняя позицию.
func FromJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("mkon: unexpected data after the JSON value")
	}
	return v, nil
}

// FromJSONStream разбирает поток JSON-значений (как вывод jq).
func FromJSONStream(data []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out []any
	for {
		v, err := decodeValue(dec)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func decodeValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			o := NewObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(k, v)
			}
			_, err := dec.Token() // '}'
			return o, err
		case '[':
			a := &Array{Items: []any{}}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				a.Items = append(a.Items, v)
			}
			_, err := dec.Token() // ']'
			return a, err
		}
		return nil, errors.New("mkon: unexpected JSON delimiter")
	case json.Number:
		f, ok := jsNumber(string(x))
		if !ok {
			return nil, errors.New("mkon: bad JSON number " + string(x))
		}
		return f, nil
	default: // string, bool, nil
		return x, nil
	}
}

// ToJSON пишет значение mkon как JSON (порядок ключей сохраняется, без экранирования HTML).
//
// В JSON нет дат и NaN/±Infinity, поэтому:
//
//	*Date              → строка в записи mkon ("2024-01-15", "2024-01-15T10:30:00Z")
//	NaN / ±Infinity    → строки "NaN", "Infinity", "-Infinity"
//	-0                 → -0
//
// indent == "" — компактный вывод.
func ToJSON(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	if err := writeJSON(&b, normalizeSafe(v), indent, ""); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func normalizeSafe(v any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(stringifyError); !ok {
				panic(r)
			}
			out = r
		}
	}()
	return normalize(v)
}

func writeJSON(b *bytes.Buffer, v any, indent, cur string) error {
	if e, ok := v.(stringifyError); ok {
		return e.error
	}
	nl := func(level string) {
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(level)
		}
	}
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			writeJSONString(b, FormatNumber(x))
		} else {
			b.WriteString(FormatNumber(x))
		}
	case string:
		writeJSONString(b, x)
	case *Date:
		s, err := FormatDate(x)
		if err != nil {
			return err
		}
		writeJSONString(b, s)
	case *Array:
		if len(x.Items) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(cur + indent)
			if err := writeJSON(b, normalizeSafe(it), indent, cur+indent); err != nil {
				return err
			}
		}
		nl(cur)
		b.WriteByte(']')
	case *Object:
		if x.Len() == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(cur + indent)
			writeJSONString(b, k)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			if err := writeJSON(b, normalizeSafe(x.values[k]), indent, cur+indent); err != nil {
				return err
			}
		}
		nl(cur)
		b.WriteByte('}')
	default:
		return errors.New("mkon: cannot write JSON for this value")
	}
	return nil
}

func writeJSONString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			default:
				if c < 0x20 {
					b.WriteString(`\u00`)
					b.WriteString(strconv.FormatInt(int64(c)>>4, 16))
					b.WriteString(strconv.FormatInt(int64(c)&15, 16))
				} else {
					b.WriteByte(c)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\u2028' || r == '\u2029' { // JS-совместимость: экранируем разделители строк
			b.WriteString(`\u202` + string("89"[r-'\u2028']))
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

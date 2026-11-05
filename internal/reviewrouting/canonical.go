// Package reviewrouting reads the additive review-routing-v1 protocol.
// Structural validation never establishes actor, complete history, or mutation authority.
package reviewrouting

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf8"
)

const (
	Version        = "review-routing-v1"
	MaxReportBytes = 262144
	MaxWireBytes   = 60000
	maxJSONBytes   = 8388608
	maxJSONDepth   = 64
	maxJSONNodes   = 65536
)

var integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// Decode preserves integer tokens, rejects duplicate keys and never repairs invalid Unicode.
// Floats are not part of this protocol. IDs above JavaScript's safe integer range
// therefore never pass through float64.
func Decode(data []byte, limit int) (any, error) {
	if limit < 1 || limit > maxJSONBytes || len(data) > limit || !utf8.Valid(data) {
		return nil, fmt.Errorf("routing JSON size or UTF-8 is invalid")
	}
	if err := checkSurrogates(data); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	value, err := decodeValue(d, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("routing JSON has trailing data")
	}
	return value, nil
}

func decodeValue(d *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > maxJSONDepth || *nodes > maxJSONNodes {
		return nil, fmt.Errorf("routing JSON nesting or node limit exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			value := map[string]any{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("routing object key is not text")
				}
				if _, exists := value[key]; exists {
					return nil, fmt.Errorf("duplicate routing JSON field %q", key)
				}
				child, err := decodeValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				value[key] = child
			}
			closeToken, err := d.Token()
			if err != nil || closeToken != json.Delim('}') {
				return nil, fmt.Errorf("routing object is incomplete")
			}
			return value, nil
		case '[':
			value := []any{}
			for d.More() {
				child, err := decodeValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				value = append(value, child)
			}
			closeToken, err := d.Token()
			if err != nil || closeToken != json.Delim(']') {
				return nil, fmt.Errorf("routing array is incomplete")
			}
			return value, nil
		default:
			return nil, fmt.Errorf("unexpected routing JSON delimiter")
		}
	case json.Number:
		if !integerPattern.MatchString(string(t)) {
			return nil, fmt.Errorf("routing JSON number must be an integer token")
		}
		return t, nil
	case string, bool, nil:
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported routing JSON value")
	}
}

// encoding/json replaces lone escaped UTF-16 surrogates with U+FFFD. Check the
// original escapes first so different input bytes cannot acquire the same digest.
func checkSurrogates(data []byte) error {
	inside := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return fmt.Errorf("incomplete routing JSON escape")
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return fmt.Errorf("incomplete routing Unicode escape")
		}
		code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid routing Unicode escape")
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return fmt.Errorf("unpaired routing low surrogate")
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return fmt.Errorf("unpaired routing high surrogate")
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired routing high surrogate")
		}
		i += 6
	}
	return nil
}

// Canonical matches Python's ensure_ascii=False, sort_keys=True, compact JSON
// followed by one LF. It preserves U+2028/U+2029 and HTML characters literally.
func Canonical(value any) ([]byte, error) {
	var out bytes.Buffer
	nodes := 0
	if err := appendCanonical(&out, value, 0, &nodes); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	if out.Len() > maxJSONBytes {
		return nil, fmt.Errorf("canonical routing JSON exceeds size limit")
	}
	return out.Bytes(), nil
}

func appendCanonical(out *bytes.Buffer, value any, depth int, nodes *int) error {
	*nodes++
	if depth > maxJSONDepth || *nodes > maxJSONNodes || out.Len() > maxJSONBytes {
		return fmt.Errorf("canonical routing JSON exceeds bounds")
	}
	switch v := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case string:
		return appendString(out, v)
	case json.Number:
		if !integerPattern.MatchString(string(v)) {
			return fmt.Errorf("routing number is not an integer")
		}
		if v == "-0" {
			out.WriteByte('0')
		} else {
			out.WriteString(string(v))
		}
	case int:
		out.WriteString(strconv.Itoa(v))
	case int64:
		out.WriteString(strconv.FormatInt(v, 10))
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := appendString(out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := appendCanonical(out, v[key], depth+1, nodes); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, child := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := appendCanonical(out, child, depth+1, nodes); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	default:
		return fmt.Errorf("unsupported canonical routing value %T", value)
	}
	return nil
}

func appendString(out *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("routing text is not strict UTF-8")
	}
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 32 {
				fmt.Fprintf(out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return nil
}

func Digest(value any) (string, error) {
	data, err := Canonical(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

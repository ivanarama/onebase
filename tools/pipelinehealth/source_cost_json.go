package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// Cost receipts use the same strict token/Unicode rules as reviewrouting's
// canonical wire reader, with the existing central HTTP response byte bound.
// Do not widen the separate routing/identity parser's8MiB contract.
const sourceCostMaxJSONDepth = 64
const sourceCostMaxJSONNodes = 65536

var sourceCostIntegerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

func sourceCostDecodeJSON(data []byte) (any, error) {
	if len(data) > maxPipelineGraphQLResponseBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("source cost JSON size or UTF-8 is invalid")
	}
	if err := sourceCostCheckSurrogates(data); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	value, err := sourceCostDecodeValue(d, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("source cost JSON has trailing data")
	}
	return value, nil
}

func sourceCostDecodeValue(d *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > sourceCostMaxJSONDepth || *nodes > sourceCostMaxJSONNodes {
		return nil, fmt.Errorf("source cost JSON nesting or node limit exceeded")
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
					return nil, fmt.Errorf("source cost object key is not text")
				}
				if _, exists := value[key]; exists {
					return nil, fmt.Errorf("duplicate source cost JSON field %q", key)
				}
				child, err := sourceCostDecodeValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				value[key] = child
			}
			closeToken, err := d.Token()
			if err != nil || closeToken != json.Delim('}') {
				return nil, fmt.Errorf("source cost object is incomplete")
			}
			return value, nil
		case '[':
			value := []any{}
			for d.More() {
				child, err := sourceCostDecodeValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				value = append(value, child)
			}
			closeToken, err := d.Token()
			if err != nil || closeToken != json.Delim(']') {
				return nil, fmt.Errorf("source cost array is incomplete")
			}
			return value, nil
		default:
			return nil, fmt.Errorf("unexpected source cost JSON delimiter")
		}
	case json.Number:
		if !sourceCostIntegerPattern.MatchString(string(t)) {
			return nil, fmt.Errorf("source cost JSON number must be an integer token")
		}
		return t, nil
	case string, bool, nil:
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported source cost JSON value")
	}
}

// encoding/json replaces lone escaped UTF-16 surrogates with U+FFFD. Check the
// original escapes first so different input bytes cannot acquire the same digest.
func sourceCostCheckSurrogates(data []byte) error {
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
			return fmt.Errorf("incomplete source cost JSON escape")
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return fmt.Errorf("incomplete source cost Unicode escape")
		}
		code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid source cost Unicode escape")
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return fmt.Errorf("unpaired source cost low surrogate")
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return fmt.Errorf("unpaired source cost high surrogate")
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired source cost high surrogate")
		}
		i += 6
	}
	return nil
}

package navigation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var deltaHash = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var deltaIcon = regexp.MustCompile(`^[a-z][a-z0-9-]{0,80}$`)

func (l Layer) prefix() (string, error) {
	switch l {
	case AdminLayer:
		return "adm:", nil
	case UserLayer:
		return "usr:", nil
	default:
		return "", fmt.Errorf("navigation: unknown layer")
	}
}

// NewCustomID is called by the server when creating a custom section/group.
func NewCustomID(layer Layer) (string, error) {
	prefix, err := layer.prefix()
	if err != nil {
		return "", err
	}
	return prefix + uuid.NewString(), nil
}

func validDeltaID(id string) bool {
	if strings.HasPrefix(id, "cfg:") {
		return len(id) > 4 && len(id) <= 128 && !strings.ContainsAny(id, " \t\r\n\x00") && utf8.ValidString(id)
	}
	if strings.HasPrefix(id, "adm:") || strings.HasPrefix(id, "usr:") {
		parsed, err := uuid.Parse(id[4:])
		return err == nil && parsed.String() == id[4:]
	}
	return false
}

// DecodeDelta rejects ambiguous JSON, unknown fields and unsupported versions.
// Its errors deliberately omit input values, which may contain personal titles.
func DecodeDelta(raw []byte, layer Layer) (Delta, error) {
	if len(raw) > MaxDeltaBytes || !utf8.Valid(raw) {
		return Delta{}, fmt.Errorf("navigation: invalid delta size or encoding")
	}
	if err := uniqueJSON(raw); err != nil {
		return Delta{}, fmt.Errorf("navigation: invalid or ambiguous JSON")
	}
	if err := exactDeltaFields(raw); err != nil {
		return Delta{}, fmt.Errorf("navigation: invalid delta fields")
	}
	var d Delta
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return Delta{}, fmt.Errorf("navigation: invalid delta fields")
	}
	if err := d.Validate(layer); err != nil {
		return Delta{}, err
	}
	return d, nil
}

func exactDeltaFields(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil || len(root) != 3 {
		return fmt.Errorf("root")
	}
	for _, key := range []string{"version", "base_hash", "ops"} {
		if _, ok := root[key]; !ok {
			return fmt.Errorf("root field")
		}
	}
	if bytes.Equal(bytes.TrimSpace(root["ops"]), []byte("null")) {
		return fmt.Errorf("operations array")
	}
	var operations []map[string]json.RawMessage
	if err := json.Unmarshal(root["ops"], &operations); err != nil {
		return err
	}
	for _, fields := range operations {
		var op string
		if err := json.Unmarshal(fields["op"], &op); err != nil {
			return err
		}
		allowed := map[string]bool{"op": true}
		switch op {
		case "rename":
			allowed["node"], allowed["title"] = true, true
		case "set_icon":
			allowed["node"], allowed["icon"] = true, true
		case "move":
			allowed["node"], allowed["parent"], allowed["after"] = true, true, true
		case "add_section", "add_group":
			allowed["id"], allowed["title"], allowed["parent"], allowed["after"] = true, true, true, true
		case "hide", "show", "remove_custom":
			allowed["node"] = true
		default:
			return fmt.Errorf("operation")
		}
		for key, value := range fields {
			// encoding/json accepts case variants and null strings. Neither is
			// part of the versioned protocol; reject them before typed decoding.
			value = bytes.TrimSpace(value)
			if !allowed[key] || len(value) == 0 || value[0] != '"' {
				return fmt.Errorf("operation field")
			}
		}
	}
	return nil
}

// EncodeDelta produces the bounded canonical form used by CAS storage.
func EncodeDelta(d Delta, layer Layer) ([]byte, error) {
	if err := d.Validate(layer); err != nil {
		return nil, err
	}
	if d.Ops == nil {
		d.Ops = []Operation{}
	}
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > MaxDeltaBytes {
		return nil, fmt.Errorf("navigation: delta exceeds size limit")
	}
	return raw, nil
}

func (d Delta) Validate(layer Layer) error {
	prefix, err := layer.prefix()
	if err != nil {
		return err
	}
	if d.Version != 1 || !deltaHash.MatchString(d.BaseHash) || len(d.Ops) > MaxOperations {
		return fmt.Errorf("navigation: invalid delta version, hash or operation count")
	}
	added := map[string]bool{}
	for index, op := range d.Ops {
		fail := func() error { return fmt.Errorf("navigation: invalid operation %d", index) }
		if op.Parent != "" && !validDeltaID(op.Parent) || op.After != "" && !validDeltaID(op.After) {
			return fail()
		}
		if op.Title != nil && !utf8.ValidString(*op.Title) || op.Icon != nil && (*op.Icon != "" && !deltaIcon.MatchString(*op.Icon)) {
			return fail()
		}
		switch op.Op {
		case "add_section", "add_group":
			if !validDeltaID(op.ID) || !strings.HasPrefix(op.ID, prefix) || added[op.ID] || op.Node != "" || op.Title == nil || strings.TrimSpace(*op.Title) == "" || op.Icon != nil {
				return fail()
			}
			if op.Op == "add_section" && op.Parent != "" || op.Op == "add_group" && op.Parent == "" {
				return fail()
			}
			if op.Parent == op.ID || op.After == op.ID {
				return fail()
			}
			added[op.ID] = true
		case "rename", "move", "hide", "show", "remove_custom", "set_icon":
			if !validDeltaID(op.Node) || op.ID != "" {
				return fail()
			}
			if op.Op != "move" && (op.Parent != "" || op.After != "") {
				return fail()
			}
			if op.Op == "move" && (op.Parent == op.Node || op.After == op.Node) {
				return fail()
			}
			if (op.Op == "rename") != (op.Title != nil) || (op.Op == "set_icon") != (op.Icon != nil) {
				return fail()
			}
			if op.Op == "remove_custom" && !strings.HasPrefix(op.Node, prefix) {
				return fail()
			}
		default:
			return fail()
		}
	}
	// Bound structured callers too, before any storage query or write.
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > MaxDeltaBytes {
		return fmt.Errorf("navigation: delta exceeds size limit")
	}
	return nil
}

// uniqueJSON walks at most a small fixed depth, rejecting duplicate keys at
// every level rather than letting encoding/json silently pick the last value.
func uniqueJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) error
	value = func(depth int) error {
		if depth > 16 {
			return fmt.Errorf("depth")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, nested := token.(json.Delim)
		if !nested {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return fmt.Errorf("duplicate key")
				}
				keys[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing value")
	}
	return nil
}

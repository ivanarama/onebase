// Package navigation defines the pure, deterministic navigation contract.
// It neither reads settings nor grants access; rendering must apply RBAC.
package navigation

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

type Target struct {
	Kind string
	Name string
	View string
}

// ParseTarget accepts typed object references only, never URLs or paths.
func ParseTarget(value string) (Target, error) {
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return Target{}, fmt.Errorf("неверный target %q: ожидается kind:name[:view]", value)
	}
	t := Target{Kind: parts[0], Name: parts[1]}
	if t.Name == "" || strings.IndexFunc(t.Name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("/\\?#%<>&", r)
	}) >= 0 || t.Name == "." || t.Name == ".." {
		return Target{}, fmt.Errorf("неверное имя в target %q", value)
	}
	switch t.Kind {
	case "register":
		if len(parts) != 3 || (parts[2] != "movements" && parts[2] != "balances") {
			return Target{}, fmt.Errorf("target %q: register требует view movements или balances", value)
		}
		t.View = parts[2]
	case "system":
		if len(parts) != 2 || t.Name != "constants" {
			return Target{}, fmt.Errorf("неизвестный системный target %q", value)
		}
	case "catalog", "document", "inforeg", "report", "processor", "journal", "page":
		if len(parts) != 2 {
			return Target{}, fmt.Errorf("target %q не поддерживает view", value)
		}
	default:
		return Target{}, fmt.Errorf("неизвестный тип target %q", value)
	}
	return t, nil
}

func (t Target) String() string {
	v := t.Kind + ":" + t.Name
	if t.View != "" {
		v += ":" + t.View
	}
	return v
}

func (t Target) key() string { return strings.ToLower(t.String()) }

// URL returns the existing runtime route. Context is escaped as a query value.
func (t Target) URL(context string) string {
	return t.routeURL(context, true)
}

// LegacyURL preserves the existing navigation DTO's raw UTF-8 names/query.
// html/template normalizes these URLs when rendering an href attribute.
func (t Target) LegacyURL(context string) string {
	return t.routeURL(context, false)
}

func (t Target) routeURL(context string, escaped bool) string {
	if t.Kind == "system" {
		return "/ui/constants"
	}
	name := t.Name
	if t.Kind != "catalog" && t.Kind != "document" && t.Kind != "page" {
		name = strings.ToLower(name)
	}
	if escaped {
		name = url.PathEscape(name)
	}
	path := "/ui/" + t.Kind + "/" + name
	if t.View == "balances" {
		path += "/balances"
	}
	if context != "" {
		if escaped {
			path += "?" + url.Values{"subsystem": {context}}.Encode()
		} else {
			path += "?subsystem=" + context
		}
	}
	return path
}

func (t Target) Action() string {
	if t.Kind == "report" || t.Kind == "processor" {
		return "run"
	}
	return "read"
}

// Ordered projects the declared list onto existing objects, keeping the first
// occurrence of each exact name. Missing objects never become navigation links.
func Ordered[T any](names []string, objects []*T, name func(*T) string) []*T {
	byName := make(map[string]*T, len(objects))
	for _, object := range objects {
		byName[name(object)] = object
	}
	var result []*T
	for _, n := range names {
		if object := byName[n]; object != nil {
			result = append(result, object)
			delete(byName, n)
		}
	}
	return result
}

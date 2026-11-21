package provenance

import (
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// FormatVersion changes when the canonical byte grammar changes. Stored
// receipts must bind this version and cannot reuse a previous format's hash.
const FormatVersion = 2

// Type.String is descriptive, not unique: unnamed containers lose the import
// paths of named children. Encode every composite component before nil/empty or
// byte fast paths. Named declarations use their full import path and name.
func (e *encoder) typeIdentity(t reflect.Type, depth int) bool {
	e.nodes++
	if e.nodes > 1<<20 || depth > 64 {
		return false
	}
	switch t.Kind() {
	case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Uintptr, reflect.Complex64, reflect.Complex128:
		return false
	}
	if t.Kind() == reflect.Interface && t.NumMethod() != 0 {
		return false
	}
	if t.Name() != "" {
		// Reflection does not expose generic type arguments as a separate API.
		// Do not claim collision-free identity for an unproved instantiated name.
		if strings.Contains(t.Name(), "[") {
			return false
		}
		return e.text("named-type") && e.text(t.PkgPath()) && e.text(t.Name()) && e.text(t.Kind().String())
	}
	if !e.text("composite-type") || !e.text(t.Kind().String()) {
		return false
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice:
		return e.typeIdentity(t.Elem(), depth+1)
	case reflect.Array:
		return e.text(strconv.Itoa(t.Len())) && e.typeIdentity(t.Elem(), depth+1)
	case reflect.Map:
		return e.typeIdentity(t.Key(), depth+1) && e.typeIdentity(t.Elem(), depth+1)
	case reflect.Interface:
		return t.NumMethod() == 0
	case reflect.Struct:
		if !e.text(strconv.Itoa(t.NumField())) {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" || !e.text(f.Name) || !e.text(string(f.Tag)) || !e.text(strconv.FormatBool(f.Anonymous)) || !e.typeIdentity(f.Type, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// Check the declared domain independently of runtime values. Empty/nil
// collections and zero-length arrays must not hide an unknown element type.
func inputTypeAllowed(t reflect.Type, depth int) bool {
	if depth > 32 {
		return false
	}
	if t == reflect.TypeOf(time.Time{}) || t == reflect.TypeOf(uuid.UUID{}) || t == reflect.TypeOf(decimal.Decimal{}) {
		return true
	}
	if t.PkgPath() != "" {
		return false
	}
	switch t.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	case reflect.Interface:
		return t.Name() == "" && t.NumMethod() == 0
	case reflect.Array:
		if t.Len() > 65536 {
			return false
		}
		fallthrough
	case reflect.Slice:
		return t.Name() == "" && inputTypeAllowed(t.Elem(), depth+1)
	case reflect.Map:
		return t.Name() == "" && t.Key() == reflect.TypeOf("") && inputTypeAllowed(t.Elem(), depth+1)
	}
	return false
}

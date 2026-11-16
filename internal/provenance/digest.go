// Package provenance hashes bounded host data with explicit Go type identity.
// A digest supplies integrity evidence, not authentication or authorization.
// Callers must keep subject bindings and trusted input origins server-side.
package provenance

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Receipts must not use JSON's equivalence classes (decimal/string, bytes/
// base64 string, typed nil, etc.). No arbitrary MarshalJSON/Valuer is called.
// Only the known SQL argument types below may enter this strict profile.
func inputsSupported(values []any) bool {
	nodes := 0
	var check func(any, int) bool
	check = func(value any, depth int) bool {
		nodes++
		if nodes > 65536 || depth > 32 {
			return false
		}
		if value != nil && !inputTypeAllowed(reflect.TypeOf(value), 0) {
			return false
		}
		switch value.(type) {
		case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, []byte, time.Time, uuid.UUID, decimal.Decimal:
			return true
		}
		v := reflect.ValueOf(value)
		if !v.IsValid() {
			return true
		}
		if v.Type().PkgPath() != "" {
			return false
		}
		switch v.Kind() {
		case reflect.Array, reflect.Slice:
			if v.Len() > 65536 {
				return false
			}
			for i := 0; i < v.Len(); i++ {
				if !check(v.Index(i).Interface(), depth+1) {
					return false
				}
			}
			return true
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String || v.Len() > 65536 {
				return false
			}
			for _, key := range v.MapKeys() {
				if !check(v.MapIndex(key).Interface(), depth+1) {
					return false
				}
			}
			return true
		}
		return false
	}
	for _, value := range values {
		if !check(value, 0) {
			return false
		}
	}
	return true
}

type encoder struct {
	h            hash.Hash
	nodes, bytes int
}

func typedDigest(value any) ([32]byte, bool) {
	e := encoder{h: sha256.New()}
	if !e.text("onebase/provenance/v2") {
		return [32]byte{}, false
	}
	if !e.value(reflect.ValueOf(value), 0) {
		return [32]byte{}, false
	}
	var result [32]byte
	copy(result[:], e.h.Sum(nil))
	return result, true
}
func (e *encoder) write(value []byte) bool {
	e.bytes += len(value) + 8
	if e.bytes > 64<<20 {
		return false
	}
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = e.h.Write(length[:])
	_, _ = e.h.Write(value)
	return true
}
func (e *encoder) text(value string) bool {
	if len(value) > 64<<20-e.bytes-8 {
		return false
	}
	return e.write([]byte(value))
}
func (e *encoder) value(v reflect.Value, depth int) bool {
	e.nodes++
	if e.nodes > 1<<20 || depth > 64 {
		return false
	}
	if !v.IsValid() {
		return e.text("untyped-nil")
	}
	if !e.typeIdentity(v.Type(), 0) {
		return false
	}
	if v.CanInterface() {
		switch value := v.Interface().(type) {
		case decimal.Decimal:
			return e.text(value.Coefficient().String()) && e.text(strconv.FormatInt(int64(value.Exponent()), 10))
		case time.Time:
			b, err := value.MarshalBinary()
			return err == nil && e.write(b) && e.text(value.Location().String())
		}
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return e.text("nil")
		}
		return e.text("value") && e.value(v.Elem(), depth+1)
	case reflect.String:
		return e.text(v.String())
	case reflect.Bool:
		return e.text(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return e.text(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return e.text(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
		return e.text(strconv.FormatUint(math.Float64bits(f), 16))
	case reflect.Array, reflect.Slice:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				return e.text("nil")
			}
			if !e.text("value") {
				return false
			}
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return e.write(v.Bytes())
			}
		}
		if v.Len() > 1<<20 || !e.text(strconv.Itoa(v.Len())) {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if !e.value(v.Index(i), depth+1) {
				return false
			}
		}
		return true
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String || v.Len() > 65536 {
			return false
		}
		if v.IsNil() {
			return e.text("nil")
		}
		if !e.text("value") || !e.text(strconv.Itoa(v.Len())) {
			return false
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, key := range keys {
			if !e.value(key, depth+1) || !e.value(v.MapIndex(key), depth+1) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if !e.text(strconv.Itoa(v.NumField())) {
			return false
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if field.PkgPath != "" || !e.text(field.Name) || !e.value(v.Field(i), depth+1) {
				return false
			}
		}
		return true
	}
	// Function/channel/unsafe/private host state is never fingerprinted as {}.
	return false
}

// ErrUnsupported never formats the rejected value or any host capability.
type digestError string

func (e digestError) Error() string { return string(e) }

const ErrUnsupported digestError = "provenance: unsupported or unbounded typed value"

// TypedDigest hashes a bounded host snapshot: exported plain structs, maps with
// string keys, arrays/slices, scalars, time.Time and decimal.Decimal. It invokes
// no MarshalJSON, Stringer or Valuer. Private fields, callbacks, non-finite
// numbers, cycles and excessive depth/size fail closed. Struct types and every
// nested value type participate in the hash. This is not a MAC or a trust grant.
func TypedDigest(value any) ([32]byte, error) {
	d, ok := typedDigest(value)
	if !ok {
		return [32]byte{}, ErrUnsupported
	}
	return d, nil
}

// InputDigest is the stricter input boundary: nil, builtin scalar types,
// time.Time/uuid.UUID/decimal.Decimal and bounded scalar arrays/string-key maps.
// Arbitrary named structs, pointers and custom SQL/JSON wrappers are rejected.
// A user-input digest does not turn that value into a fixed server literal.
func InputDigest(value any) ([32]byte, error) {
	if !inputsSupported([]any{value}) {
		return [32]byte{}, ErrUnsupported
	}
	return TypedDigest(value)
}

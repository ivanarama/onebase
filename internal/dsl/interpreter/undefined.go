package interpreter

// undefinedValue is confined to expression results and interpreter frames.
// Collections and host interfaces retain nil, so no recursive conversion or
// copying of mutable/cyclic user values is needed at the host boundary.
type undefinedValue uint8

const undefined undefinedValue = 0

// IsUndefined recognizes semantic absence in both interpreter and host values.
// A typed empty field (zero, false, empty string/date/reference) is defined.
func IsUndefined(value any) bool {
	if value == nil {
		return true
	}
	_, ok := value.(undefinedValue)
	return ok
}

// FromHostValue materializes a host nil as the internal Неопределено value.
// It deliberately does not infer field types; that belongs to metadata adapters.
func FromHostValue(value any) any {
	if value == nil {
		return undefined
	}
	return value
}

// ToHostValue preserves the nil contract of callbacks, object setters, native
// collections, serializers, query parameters, storage and public Go results.
func ToHostValue(value any) any {
	if IsUndefined(value) {
		return nil
	}
	return value
}

package interpreter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

type sandboxStringVisit struct {
	kind byte
	ptr  uintptr
	len  int
}

type sandboxStringBudget struct {
	operation  string
	line       int
	maxDecimal int32
	maxBytes   int
	total      int
	nodes      int
	seen       map[sandboxStringVisit]bool
}

func requireSafeSandboxStringValues(operation string, values []any, maxBytes, line int) {
	requireSafeSandboxValueLimits(operation, values, 0, maxBytes, line)
}

// requireSafeSandboxValueLimits walks native value graphs without invoking
// their formatting methods. Decimal checks happen during this walk, before a
// string preflight is allowed to call fmt on an Array/Struct argument.
func requireSafeSandboxValueLimits(operation string, values []any, maxDecimal int32, maxBytes, line int) {
	requireSafeSandboxValueLimitsWithDecimal(operation, values, maxDecimal, maxBytes, line)
}

func requireSafeSandboxFormattingValueLimits(operation string, values []any, maxDecimal int32, maxBytes, line int) {
	requireSafeSandboxValueLimitsWithDecimal(operation, values, sandboxEffectiveDecimalBound(maxDecimal, maxBytes), maxBytes, line)
}

func requireSafeSandboxValueLimitsWithDecimal(operation string, values []any, decimalBound int32, maxBytes, line int) {
	if decimalBound <= 0 && maxBytes <= 0 {
		return
	}
	b := &sandboxStringBudget{
		operation:  operation,
		line:       line,
		maxDecimal: decimalBound,
		maxBytes:   maxBytes,
		seen:       make(map[sandboxStringVisit]bool),
	}
	b.visit(values, 0)
}

func (b *sandboxStringBudget) fail(detail string) {
	panic(userError{
		Msg:  fmt.Sprintf("%s: %s exceeds the sandbox string limit %d bytes", b.operation, detail, b.maxBytes),
		Line: b.line,
	})
}

func sandboxStringNodeLimit(maxBytes int) int {
	if maxBytes <= 0 {
		return 128 << 10
	}
	limit := maxBytes / 8
	if limit < 1024 {
		limit = 1024
	}
	return limit
}

func (b *sandboxStringBudget) addBytes(n int) {
	if b.maxBytes <= 0 {
		return
	}
	if n < 0 || n > b.maxBytes-b.total {
		b.fail("total string data")
	}
	b.total += n
}

func (b *sandboxStringBudget) enter(depth int) {
	if depth > 64 {
		panic(userError{Msg: b.operation + ": value nesting exceeds the sandbox limit", Line: b.line})
	}
	b.nodes++
	if b.nodes > sandboxStringNodeLimit(b.maxBytes) {
		panic(userError{Msg: b.operation + ": value count exceeds the sandbox limit", Line: b.line})
	}
}

func (b *sandboxStringBudget) mark(visit sandboxStringVisit) bool {
	if b.seen[visit] {
		return false
	}
	b.seen[visit] = true
	return true
}

func (b *sandboxStringBudget) visit(value any, depth int) {
	b.enter(depth)
	// Charge textual input before any numeric parser gets a chance to allocate
	// a coefficient for an oversized digit string.
	switch text := value.(type) {
	case string:
		b.addBytes(len(text))
	case json.Number:
		b.addBytes(len(text))
	case []byte:
		b.addBytes(len(text))
	}
	if b.maxDecimal > 0 {
		requireSafeSandboxNumber(b.operation, value, b.maxDecimal, b.line)
	}
	switch v := value.(type) {
	case string, json.Number, []byte:
		// Already charged above.
	case *Array:
		if v == nil || !b.mark(sandboxStringVisit{kind: 'a', ptr: reflect.ValueOf(v).Pointer()}) {
			return
		}
		for _, item := range v.items {
			b.visit(item, depth+1)
		}
	case *Map:
		if v == nil || !b.mark(sandboxStringVisit{kind: 'm', ptr: reflect.ValueOf(v).Pointer()}) {
			return
		}
		for _, item := range v.keys {
			b.visit(item, depth+1)
		}
		for _, item := range v.vals {
			b.visit(item, depth+1)
		}
	case *Struct:
		if v == nil || !b.mark(sandboxStringVisit{kind: 't', ptr: reflect.ValueOf(v).Pointer()}) {
			return
		}
		for _, name := range v.keys {
			b.addBytes(len(name))
			b.visit(v.vals[name], depth+1)
		}
	case *readOnlyThis:
		// Final #916 represents a protected native Struct as readOnlyThis.
		// Inspect only that inert snapshot; arbitrary host This values remain
		// opaque and must never be reached through their Get callbacks here.
		if v != nil {
			if native, ok := v.inner.(*Struct); ok {
				b.visit(native, depth+1)
			}
		}
	case []any:
		if v == nil || !b.mark(sandboxStringVisit{kind: 's', ptr: reflect.ValueOf(v).Pointer(), len: len(v)}) {
			return
		}
		for _, item := range v {
			b.visit(item, depth+1)
		}
	case map[string]any:
		if v == nil || !b.mark(sandboxStringVisit{kind: 'r', ptr: reflect.ValueOf(v).Pointer()}) {
			return
		}
		for name, item := range v {
			b.addBytes(len(name))
			b.visit(item, depth+1)
		}
	default:
		reflected := reflect.ValueOf(value)
		if reflected.IsValid() && reflected.Kind() == reflect.String {
			b.addBytes(reflected.Len())
		}
	}
}

func requireSandboxStringLength(operation string, length, maxBytes, line int) {
	if length >= 0 && length <= maxBytes {
		return
	}
	panic(userError{
		Msg:  fmt.Sprintf("%s: result exceeds the sandbox string limit %d bytes", operation, maxBytes),
		Line: line,
	})
}

func sandboxBuiltinStringArg(operation string, args []any, index, maxBytes, line int) string {
	s := strArg(args, index)
	requireSandboxStringLength(operation, len(s), maxBytes, line)
	return s
}

func sandboxConcatValues(ec *execCtx, operation string, left, right any, line int) string {
	maxBytes := 0
	maxDecimal := int32(0)
	if ec != nil {
		maxBytes = ec.maxStringExpansion
		maxDecimal = ec.maxDecimalExpansion
	}
	requireSafeSandboxFormattingValueLimits(operation, []any{left, right}, maxDecimal, maxBytes, line)
	format := func(value any) string {
		if s, ok := value.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", value)
	}
	leftString := format(left)
	rightString := format(right)
	if maxBytes > 0 {
		requireSandboxStringLength(operation, len(leftString), maxBytes, line)
		requireSandboxStringLength(operation, len(rightString), maxBytes, line)
		sandboxExpandedLength(operation, len(leftString), 1, 0, len(rightString), maxBytes, line)
	}
	return leftString + rightString
}

func sandboxBuiltinFormatsValues(operation string) bool {
	switch strings.ToLower(operation) {
	case "str", "строка", "format", "формат",
		"upper", "врег", "lower", "нрег", "trimall", "сокрлп",
		"left", "лев", "right", "прав", "mid", "сред", "strlen", "стрдлина",
		"strfind", "стрнайти", "strreplace", "стрзаменить",
		"strstartswith", "стрначинаетсяс", "strendswith", "стрзаканчиваетсяна",
		"strcontains", "стрсодержит", "strsplit", "стрразделить", "strjoin", "стрсоединить",
		"strtemplate", "стршаблон", "trimleft", "сокрл", "trimright", "сокрп",
		"stroccurrencecount", "стрчисловхождений", "strlinecount", "стрчислострок",
		"strgetline", "стрполучитьстроку", "strcompare", "стрсравнить",
		"isblankstring", "пустаястрока", "titlecase", "трег", "nstr", "нстр",
		"readjson", "прочитатьjson", "writejson", "записатьjson":
		return true
	default:
		return false
	}
}

func sandboxExpandedLength(operation string, base, count, removed, added, maxBytes, line int) int {
	if base < 0 || count < 0 || removed < 0 || added < 0 || base > maxBytes {
		requireSandboxStringLength(operation, maxBytes+1, maxBytes, line)
	}
	result := base
	if added >= removed {
		delta := added - removed
		if delta > 0 && count > (maxBytes-result)/delta {
			requireSandboxStringLength(operation, maxBytes+1, maxBytes, line)
		}
		result += count * delta
	} else {
		result -= count * (removed - added)
	}
	requireSandboxStringLength(operation, result, maxBytes, line)
	return result
}

func preflightSandboxStringBuiltin(operation string, args []any, maxBytes, line int) {
	switch strings.ToLower(operation) {
	case "strreplace", "стрзаменить":
		source := sandboxBuiltinStringArg(operation, args, 0, maxBytes, line)
		old := sandboxBuiltinStringArg(operation, args, 1, maxBytes, line)
		replacement := sandboxBuiltinStringArg(operation, args, 2, maxBytes, line)
		count := strings.Count(source, old)
		sandboxExpandedLength(operation, len(source), count, len(old), len(replacement), maxBytes, line)
	case "strjoin", "стрсоединить":
		preflightSandboxJoin(operation, args, maxBytes, line)
	case "strsplit", "стрразделить":
		source := sandboxBuiltinStringArg(operation, args, 0, maxBytes, line)
		separator := sandboxBuiltinStringArg(operation, args, 1, maxBytes, line)
		parts := strings.Count(source, separator) + 1
		if separator == "" {
			parts = utf8.RuneCountInString(source)
		}
		requireSandboxStringParts(operation, parts, maxBytes, line)
	case "strgetline", "стрполучитьстроку":
		source := sandboxBuiltinStringArg(operation, args, 0, maxBytes, line)
		requireSandboxStringParts(operation, strings.Count(source, "\n")+1, maxBytes, line)
	case "nstr", "нстр":
		source := sandboxBuiltinStringArg(operation, args, 0, maxBytes, line)
		requireSandboxStringParts(operation, strings.Count(source, ";")+1, maxBytes, line)
	}
}

func requireSandboxStringParts(operation string, parts, maxBytes, line int) {
	if parts >= 0 && parts <= sandboxStringNodeLimit(maxBytes) {
		return
	}
	panic(userError{
		Msg:  fmt.Sprintf("%s: result has too many string elements for the sandbox limit", operation),
		Line: line,
	})
}

func preflightSandboxJoin(operation string, args []any, maxBytes, line int) {
	separator := sandboxBuiltinStringArg(operation, args, 1, maxBytes, line)
	var values []any
	if len(args) > 0 {
		switch array := args[0].(type) {
		case *Array:
			if array != nil {
				values = array.items
			}
		case []any:
			values = array
		}
	}
	requireSandboxStringParts(operation, len(values), maxBytes, line)
	total := 0
	for _, value := range values {
		part := fmt.Sprintf("%v", value)
		requireSandboxStringLength(operation, len(part), maxBytes, line)
		if len(part) > maxBytes-total {
			requireSandboxStringLength(operation, maxBytes+1, maxBytes, line)
		}
		total += len(part)
	}
	if len(values) > 1 {
		sandboxExpandedLength(operation, total, len(values)-1, 0, len(separator), maxBytes, line)
	}
}

func sandboxTemplateBuiltin(operation string) bool {
	switch strings.ToLower(operation) {
	case "strtemplate", "стршаблон":
		return true
	default:
		return false
	}
}

func sandboxTemplateBounded(operation string, args []any, maxBytes, line int) string {
	// 1C templates expose placeholders %1..%10. Keeping the sandbox to that
	// contract bounds the number of full-string scans. Execute the same
	// largest-index-first algorithm as the builtin, but calculate and validate
	// every next intermediate before ReplaceAll allocates it. Re-counting the
	// current string is essential: a replacement can form a lower marker across
	// its boundary with surrounding text (for example "%21" with %2 -> "%").
	if len(args) > 11 {
		panic(userError{Msg: operation + ": too many template arguments in sandbox", Line: line})
	}
	result := sandboxBuiltinStringArg(operation, args, 0, maxBytes, line)
	for i := len(args) - 1; i >= 1; i-- {
		marker := fmt.Sprintf("%%%d", i)
		replacement := fmt.Sprintf("%v", args[i])
		requireSandboxStringLength(operation, len(replacement), maxBytes, line)
		occurrences := strings.Count(result, marker)
		sandboxExpandedLength(operation, len(result), occurrences, len(marker), len(replacement), maxBytes, line)
		if occurrences > 0 {
			result = strings.ReplaceAll(result, marker, replacement)
		}
	}
	return result
}

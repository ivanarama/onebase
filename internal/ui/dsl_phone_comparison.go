package ui

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Trusted server handlers use request-local HMACs for phone comparison.
func (s *Server) bindPhoneComparison(vars map[string]any, ctx func() context.Context, fieldPath string) {
	entityName, fieldName, ok := strings.Cut(fieldPath, ".")
	if !ok {
		return
	}
	entity := s.reg.GetEntity(entityName)
	if entity == nil || entity.Kind != metadata.KindDocument {
		return
	}
	field, ok := entityFieldByName(entity, fieldName)
	if !ok || !field.PII || field.Type != metadata.FieldTypeString {
		return
	}

	comparison := &phoneComparison{key: randBytes()}
	comparison.documentKeys = func(ids []uuid.UUID) (map[string]string, error) {
		decision, err := s.rowDecision(ctx(), entity, "read")
		if err != nil || !decision.Allowed {
			return nil, fmt.Errorf("нет доступа к записям для сравнения телефонов")
		}
		result := make(map[string]string, len(ids))
		// Keep parameter counts comfortably below both PostgreSQL and SQLite limits.
		for start := 0; start < len(ids); start += 500 {
			batch := ids[start:min(start+500, len(ids))]
			rows, err := s.store.GetFieldsByIDsFiltered(ctx(), entity, batch, []metadata.Field{field}, decision.Predicate)
			if err != nil || len(rows) != len(batch) {
				return nil, fmt.Errorf("не удалось прочитать доступные записи для сравнения телефонов")
			}
			for id, row := range rows {
				number, _ := row[field.Name].(string)
				result[id] = comparison.phoneKey(number)
			}
		}
		return result, nil
	}
	vars["СопоставлениеТелефонов"] = comparison
	vars["PhoneComparison"] = comparison
}

func randBytes() [32]byte {
	var key [32]byte
	// With Go 1.24+ crypto/rand.Read either fills its buffer or terminates.
	_, _ = rand.Read(key[:])
	return key
}

type phoneComparison struct {
	key          [32]byte
	documentKeys func([]uuid.UUID) (map[string]string, error)
}

// Do not print the secret in DSL diagnostics or string formatting.
func (*phoneComparison) String() string   { return "PhoneComparison" }
func (*phoneComparison) GoString() string { return "PhoneComparison" }

func (*phoneComparison) KnownMethods() (string, []string) {
	return "СопоставлениеТелефонов", []string{"Ключ", "КлючиДокументов", "Key", "DocumentKeys"}
}

func (c *phoneComparison) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "ключ", "key":
		if len(args) == 1 {
			if number, ok := args[0].(string); ok {
				return c.phoneKey(number)
			}
		}
		interpreter.RaiseUserError("Ключ телефона принимает строку")
	case "ключидокументов", "documentkeys":
		if len(args) == 1 {
			if items, ok := args[0].(*interpreter.Array); ok {
				return c.keysForArray(items)
			}
		}
		interpreter.RaiseUserError("Ключи телефонов документов принимают массив идентификаторов")
	}
	return nil
}

func (c *phoneComparison) keysForArray(items *interpreter.Array) any {
	values := items.Iterate()
	ids := make([]uuid.UUID, 0, len(values))
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, v := range values {
		id, err := uuid.Parse(fmt.Sprint(v))
		if err != nil || id == uuid.Nil {
			interpreter.RaiseUserError("неверный идентификатор документа для сравнения телефонов")
		}
		if _, duplicate := seen[id]; !duplicate {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	keys, err := c.documentKeys(ids)
	if err != nil {
		interpreter.RaiseUserError(err.Error())
	}
	return interpreter.NewStringMap(keys)
}

func (c *phoneComparison) phoneKey(number string) string {
	// Same leading-7 convention for both sources. Preserve short and
	// nonstandard identifiers as-is.
	if utf8.RuneCountInString(number) >= 11 && strings.HasPrefix(number, "7") {
		number = string([]rune(number)[1:11])
	}
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = mac.Write([]byte(number))
	return "p:" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

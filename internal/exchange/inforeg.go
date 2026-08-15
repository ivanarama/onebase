package exchange

// Регистры сведений в составе обмена (план 86, фаза 2). Синхронизируются
// НЕЗАВИСИМЫЕ записи регистра сведений (введённые формой регистра, без recorder-
// документа; движения проведения — это фаза с переносом движений). Запись
// идентифицируется каноничным ключом измерений; у неё нет _version, поэтому
// идемпотентность и порядок опираются на «водяной знак» _exchange_applied.
//
// Периодические регистры пока не поддержаны: их ключ включает period (time.Time),
// а смешение форменной (локальная зона) и обменной (UTC) записи делает сравнение
// периода на SQLite ненадёжным. Такой состав пропускается регистрацией и
// помечается предупреждением configcheck.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

// RegisterInfoRegOnSave регистрирует изменение записи регистра сведений во всех
// планах обмена, где регистр в составе (РегистрСведений.X). deletion=true — запись
// удалена. Вызывается в транзакции записи/удаления записи из формы регистра.
func RegisterInfoRegOnSave(ctx context.Context, store *storage.DB, plans []*metadata.ExchangePlan, ir *metadata.InfoRegister, dims map[string]any, deletion bool) error {
	if store == nil || ir == nil || len(plans) == 0 || ir.Periodic {
		return nil
	}
	var key string
	changedAt := time.Now().UnixMilli()
	for _, plan := range plans {
		if !plan.IncludesInfoRegister(ir.Name) {
			continue
		}
		if err := validateExchangePlan(plan); err != nil {
			return err
		}
		thisNode, err := store.GetExchangeThisNode(ctx, plan.Name)
		if err != nil {
			return err
		}
		if thisNode == "" {
			continue
		}
		thisNodeDef := plan.Node(thisNode)
		if thisNodeDef == nil {
			return fmt.Errorf("exchange: текущий узел %q не описан в плане %q", thisNode, plan.Name)
		}
		thisNode = thisNodeDef.Code
		targets := plan.RegistrationTargets(thisNode)
		if len(targets) == 0 {
			continue
		}
		if key == "" {
			keyDims := dims
			if !deletion {
				stored, err := store.InfoRegGetExactWithKeyValues(ctx, ir, dims, nil)
				if err != nil {
					return fmt.Errorf("exchange: read back information register %s key: %w", ir.Name, err)
				}
				if stored == nil {
					return fmt.Errorf("exchange: information register %s disappeared before key registration", ir.Name)
				}
				keyDims, err = storedInfoRegKeyDimensions(store, ir, stored)
				if err != nil {
					return err
				}
			}
			key = encodeInfoRegKey(store, ir, keyDims)
		}
		for _, target := range targets {
			if err := store.RegisterExchangeChange(ctx, storage.ExchangeChange{
				Plan:       plan.Name,
				ObjectType: ir.Name,
				ObjectID:   key,
				NodeCode:   target,
				Kind:       storage.ExchangeKindInfoReg,
				Version:    changedAt,
				Deletion:   deletion,
				ChangedAt:  changedAt,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func storedInfoRegKeyDimensions(store *storage.DB, ir *metadata.InfoRegister, row map[string]any) (map[string]any, error) {
	dims := make(map[string]any, len(ir.Dimensions))
	keyValues, _ := row[storage.InfoRegKeyValuesField].(map[string]string)
	for _, field := range ir.Dimensions {
		if store.IsSQLite() && (field.Type == metadata.FieldTypeNumber || field.Type == metadata.FieldTypeDate) {
			value, ok := keyValues[field.Name]
			if !ok {
				return nil, fmt.Errorf("exchange: information register %s readback omitted key %q", ir.Name, field.Name)
			}
			dims[field.Name] = value
			continue
		}
		value, ok := row[field.Name]
		if !ok {
			return nil, fmt.Errorf("exchange: information register %s readback omitted dimension %q", ir.Name, field.Name)
		}
		dims[field.Name] = value
	}
	return dims, nil
}

// encodeInfoRegKey строит детерминированный ключ записи из каноничных значений
// измерений (json.Marshal сортирует ключи map — порядок стабилен). Ключ служит и
// object_id строки очереди, и способом восстановить измерения при сборке пакета.
func encodeInfoRegKey(store *storage.DB, ir *metadata.InfoRegister, dims map[string]any) string {
	row := canonicalRow(ir.Dimensions, dims)
	if store != nil {
		for _, field := range ir.Dimensions {
			switch {
			case store.IsPostgres() && field.Type == metadata.FieldTypeNumber:
				if value, ok := canonicalPostgresInfoRegNumber(dims[field.Name]); ok {
					row[field.Name] = value
				}
			case store.IsPostgres() && field.Type == metadata.FieldTypeDate:
				if value, ok := canonicalPostgresInfoRegDate(dims[field.Name]); ok {
					row[field.Name] = value
				}
			case store.IsSQLite() && field.Type == metadata.FieldTypeDate:
				if value, ok := canonicalSQLiteInfoRegDate(dims[field.Name]); ok {
					row[field.Name] = value
				}
			}
		}
	}
	b, _ := json.Marshal(row)
	return string(b)
}

func canonicalPostgresInfoRegNumber(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	var raw string
	switch typed := value.(type) {
	case string:
		raw = typed
	case []byte:
		raw = string(typed)
	default:
		raw = fmt.Sprint(value)
	}
	number, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	return number.String(), true
}

func canonicalPostgresInfoRegDate(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	if typed, ok := value.(time.Time); ok {
		return canonicalPostgresInfoRegTime(typed), true
	}
	if typed, ok := value.(*time.Time); ok {
		if typed == nil {
			return "", false
		}
		return canonicalPostgresInfoRegTime(*typed), true
	}
	var raw string
	switch typed := value.(type) {
	case string:
		raw = typed
	case []byte:
		raw = string(typed)
	default:
		return "", false
	}
	parsed, ok := storage.ParseRegPeriod(raw)
	if !ok {
		return "", false
	}
	return canonicalPostgresInfoRegTime(parsed), true
}

func canonicalPostgresInfoRegTime(value time.Time) string {
	// PostgreSQL timestamps have microsecond storage precision. Values read back
	// from the database already satisfy it; truncation also makes deletion keys
	// built from a Go time.Time follow pgx's binary encoder.
	return value.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

const infoRegSQLiteTimeLayout = "2006-01-02 15:04:05-07:00"

func canonicalSQLiteInfoRegDate(value any) (string, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC().Format(infoRegSQLiteTimeLayout), true
	case *time.Time:
		if typed == nil {
			return "", false
		}
		return typed.UTC().Format(infoRegSQLiteTimeLayout), true
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}

func decodeInfoRegKey(key string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(key), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// applyInfoReg идемпотентно применяет запись регистра сведений из пакета.
// Конфликт (встречная неотправленная правка источнику) разрешается правилом плана
// (by_time/by_node_priority; hook к записи-без-структуры не применяется). Возвращает
// true, если запись была применена/удалена (для транзитной ретрансляции хабом).
func applyInfoReg(ctx context.Context, store *storage.DB, resolver EntityResolver, plan *metadata.ExchangePlan, thisNode, fromNode string, obj PackageObject, res *LoadResult) (bool, error) {
	ir := resolver.GetInfoRegister(obj.Type)
	if ir == nil || ir.Periodic {
		res.Skipped++ // регистр неизвестен приёмнику или периодический (не поддержан)
		return false, nil
	}
	dims := make(map[string]any, len(ir.Dimensions))
	for _, df := range ir.Dimensions {
		dims[df.Name] = obj.Fields[df.Name]
	}
	resources := make(map[string]any, len(ir.Resources))
	for _, rf := range ir.Resources {
		resources[rf.Name] = obj.Fields[rf.Name]
	}

	apply := func() error {
		if err := store.InfoRegApplyExchange(ctx, ir, dims, resources, nil, obj.Deletion); err != nil {
			return err
		}
		if err := store.SetExchangeApplied(ctx, plan.Name, obj.Type, obj.ID, obj.ChangedAt); err != nil {
			return err
		}
		if obj.Deletion {
			res.Deleted++
		} else {
			res.Applied++
		}
		return nil
	}

	local, hasLocal, err := store.GetExchangeChange(ctx, plan.Name, obj.Type, obj.ID, fromNode)
	if err != nil {
		return false, err
	}
	if hasLocal {
		res.Conflicts++
		if !resolveScalarConflict(plan, thisNode, fromNode, obj.ChangedAt, local.ChangedAt) {
			res.Skipped++ // локальное изменение победило
			return false, nil
		}
		if err := apply(); err != nil {
			return false, err
		}
		if err := store.DeleteExchangeChange(ctx, plan.Name, obj.Type, obj.ID, fromNode); err != nil {
			return false, err
		}
		return true, nil
	}
	at, ok, err := store.ExchangeAppliedAt(ctx, plan.Name, obj.Type, obj.ID)
	if err != nil {
		return false, err
	}
	if ok && obj.ChangedAt <= at {
		res.Skipped++
		return false, nil
	}
	return true, apply()
}

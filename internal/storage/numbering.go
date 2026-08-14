package storage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Единая точка автонумерации (план 117C). До неё логика «взять период → взять
// следующий номер → отформатировать» жила в пяти местах: UI, REST, DSL-объект
// документа, ИИ-действия и builtin Нумераторы. Копии уже расходились — #359 и
// Д13 плана 117 — и расходились молча: номер просто получался другим, а какой
// путь его выдал, по данным не видно.
//
// Здесь же чинится Д13: дата периода выбиралась перебором map полей, то есть у
// документа с ДВУМЯ датами счётчик был недетерминирован — в один прогон период
// брался из «Даты», в другой из «ДатыОплаты». Теперь дата выбирается по
// объявленному порядку реквизитов, с приоритетом стандартных имён.

// numberDateFields — имена, которые считаются датой документа в первую очередь.
var numberDateFields = []string{"Дата", "Period", "Период", "Date"}

// NumberPeriodDate выбирает дату, по которой считается период нумерации.
// Порядок детерминирован: сначала стандартные имена, затем первый реквизит
// типа «дата» в порядке ОБЪЯВЛЕНИЯ, затем текущее время.
func NumberPeriodDate(entity *metadata.Entity, fields map[string]any) time.Time {
	pick := func(name string) (time.Time, bool) {
		v, ok := fields[name]
		if !ok {
			for k, vv := range fields {
				if strings.EqualFold(k, name) {
					v, ok = vv, true
					break
				}
			}
		}
		if !ok {
			return time.Time{}, false
		}
		t, ok := v.(time.Time)
		return t, ok && !t.IsZero()
	}
	for _, name := range numberDateFields {
		if t, ok := pick(name); ok {
			return t
		}
	}
	if entity != nil {
		for _, f := range entity.Fields {
			if f.Type != metadata.FieldTypeDate {
				continue
			}
			if t, ok := pick(f.Name); ok {
				return t
			}
		}
	}
	return time.Now()
}

// PeriodKeyFor строит ключ счётчика по детерминированно выбранной дате.
func PeriodKeyFor(entity *metadata.Entity, num *metadata.Numerator, fields map[string]any) string {
	if num == nil {
		return ""
	}
	var periodPart string
	switch strings.ToLower(num.Period) {
	case "", "none":
	case "month":
		periodPart = NumberPeriodDate(entity, fields).Format("2006-01")
	case "day":
		periodPart = NumberPeriodDate(entity, fields).Format("2006-01-02")
	default: // year
		periodPart = NumberPeriodDate(entity, fields).Format("2006")
	}
	if num.Scope == "" {
		return periodPart
	}
	scopeVal := ""
	if v, ok := fields[num.Scope]; ok && v != nil {
		scopeVal = strings.TrimSpace(formatScopeValue(v))
	}
	if periodPart == "" {
		return scopeVal
	}
	return periodPart + "|" + scopeVal
}

// ExpandPrefix подставляет маски даты в префикс: {YYYY}, {YY}, {MM}, {DD}.
// DEVELOPER.md обещал их с плана 07, а кода не было — обещание, которое никогда
// не работало, ничем не лучше тихого no-op.
func ExpandPrefix(prefix string, date time.Time) string {
	if !strings.ContainsRune(prefix, '{') {
		return prefix
	}
	r := strings.NewReplacer(
		"{YYYY}", date.Format("2006"),
		"{YY}", date.Format("06"),
		"{MM}", date.Format("01"),
		"{DD}", date.Format("02"),
	)
	return r.Replace(prefix)
}

// GenerateNumber выдаёт следующий номер (документ) или код (справочник) по
// блоку numerator сущности. Единственная точка: все пути записи зовут её.
func (db *DB) GenerateNumber(ctx context.Context, entity *metadata.Entity, fields map[string]any) (string, error) {
	if entity == nil || entity.Numerator == nil {
		return "", nil
	}
	num := entity.Numerator
	date := NumberPeriodDate(entity, fields)
	n, err := db.NextNumber(ctx, entity.Name, PeriodKeyFor(entity, num, fields))
	if err != nil {
		return "", err
	}
	prefix := ExpandPrefix(num.Prefix, date)
	// Префикс базы идёт ПЕРЕД префиксом объекта: сначала «откуда», потом «что».
	// Подставляется только по явному base_prefix: true — иначе включение
	// префикса на базе молча изменило бы формат всех номеров сразу.
	if num.BasePrefix {
		prefix = db.GetBasePrefix(ctx) + prefix
	}
	return FormatNumber(prefix, num.Length, n), nil
}

// AutoNumberField возвращает имя реквизита, который заполняет автонумерация:
// «Номер» у документа, «Код» у справочника.
//
// Разница между видами намеренная. У документа номер выдаётся ВСЕГДА — есть
// блок numerator: или нет: без него работает legacy-счётчик, и так было с
// первых версий. У справочника — только при объявленном numerator:, потому что
// кодов у справочников не было вовсе, и раздача их всем подряд молча изменила
// бы данные всех существующих конфигураций.
func AutoNumberField(entity *metadata.Entity) string {
	if entity == nil {
		return ""
	}
	switch entity.Kind {
	case metadata.KindDocument:
		return "Номер"
	case metadata.KindCatalog:
		if entity.Numerator != nil {
			return metadata.StandardCodeField
		}
	}
	return ""
}

func formatScopeValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case time.Time:
		return t.Format(time.RFC3339)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// SetAutoNumberValue проставляет значение автонумерации ОДНОЙ колонке.
//
// Узкая функция намеренно: дозаполнение кода не должно идти через Upsert,
// который пишет запись целиком — не перечисленные в карте реквизиты он обнулил
// бы, а ссылки в прочитанной строке приходят представлением, а не UUID. Здесь
// меняется ровно один столбец, остальные данные не участвуют.
//
// expected — точное пустое значение, которое прочитал вызывающий код; nil
// означает SQL NULL. Сравнение делается в том же UPDATE, поэтому параллельная
// запись не перетирается. Возвращаемое значение сообщает, была ли строка
// действительно изменена.
func (db *DB) SetAutoNumberValue(ctx context.Context, entity *metadata.Entity, id uuid.UUID, field string, expected *string, value string) (bool, error) {
	if entity == nil || field == "" {
		return false, nil
	}
	var col string
	for _, f := range entity.Fields {
		if strings.EqualFold(f.Name, field) {
			col = metadata.ColumnName(f)
			break
		}
	}
	if col == "" {
		return false, fmt.Errorf("%s: нет реквизита %s", entity.Name, field)
	}
	d := db.dialect
	condition := col + " IS NULL"
	args := []any{value, idArg(d, id)}
	if expected != nil {
		condition = fmt.Sprintf("%s = %s", col, d.Placeholder(3))
		args = append(args, *expected)
	}
	q := fmt.Sprintf("UPDATE %s SET %s = %s WHERE id = %s AND %s",
		metadata.TableName(entity.Name), col, d.Placeholder(1), d.Placeholder(2), condition)
	tag, err := db.Exec(ctx, q, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected == 1, nil
}

// ─── Префикс базы (план 117D) ────────────────────────────────────────────────
//
// Префикс живёт в ДАННЫХ базы, а не в конфигурации, и это существо решения:
// конфигурация одинакова во всех базах, поэтому «понять, из какой базы
// загружен объект» через неё невозможно by design — обе выдали бы один и тот
// же префикс. В 1С это работает так же: префиксацию даёт не платформа, а
// константа информационной базы.
//
// Отсюда же следует, что при восстановлении базы из копии в ДРУГУЮ базу
// префикс надо гасить: иначе клон выдавал бы коды оригинала, и обмен склеил бы
// разные объекты.

const basePrefixKey = "base.prefix"

// GetBasePrefix возвращает префикс этой базы («» — не задан).
func (db *DB) GetBasePrefix(ctx context.Context) string {
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+db.dialect.Placeholder(1), basePrefixKey).Scan(&v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// SaveBasePrefix сохраняет префикс этой базы. Пустая строка снимает его.
func (db *DB) SaveBasePrefix(ctx context.Context, prefix string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		db.dialect.Placeholder(1), db.dialect.Placeholder(2))
	if _, err := db.Exec(ctx, q, basePrefixKey, strings.TrimSpace(prefix)); err != nil {
		return fmt.Errorf("settings: save %s: %w", basePrefixKey, err)
	}
	return nil
}

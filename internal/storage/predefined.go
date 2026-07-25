package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// EnsurePredefinedColumns adds _predefined_name and _is_predefined columns to
// catalog tables that declare predefined items. Safe to call repeatedly.
func (db *DB) EnsurePredefinedColumns(ctx context.Context, entities []*metadata.Entity) error {
	d := db.dialect
	for _, e := range entities {
		if e.Kind != metadata.KindCatalog || len(e.Predefined) == 0 {
			continue
		}
		table := metadata.TableName(e.Name)
		if err := db.AddColumnIfMissing(ctx, table, "_predefined_name", d.TypeText()); err != nil {
			return fmt.Errorf("ensure predefined cols %s._predefined_name: %w", e.Name, err)
		}
		if err := db.AddColumnIfMissing(ctx, table, "_is_predefined", d.TypeBool()+" NOT NULL DEFAULT "+boolFalseLit(d)); err != nil {
			return fmt.Errorf("ensure predefined cols %s._is_predefined: %w", e.Name, err)
		}
		// Partial unique index — both PG and SQLite support WHERE on indexes.
		// Boolean literal differs: TRUE for PG, 1 for SQLite.
		boolTrue := "TRUE"
		if d.Name() == "sqlite" {
			boolTrue = "1"
		}
		idxName := "idx_" + strings.ToLower(e.Name) + "_predefined"
		idxSQL := fmt.Sprintf(
			`CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (_predefined_name) WHERE _is_predefined = %s`,
			idxName, table, boolTrue)
		if _, err := db.Exec(ctx, idxSQL); err != nil {
			return fmt.Errorf("ensure predefined index %s: %w", e.Name, err)
		}
	}
	return nil
}

// isPredefinedRecord reports whether id belongs to a predefined catalog item.
// Most entity tables (including every document table) do not have the optional
// _is_predefined column, so its presence must be checked before querying it.
// In PostgreSQL an ignored "column does not exist" error aborts the surrounding
// transaction and makes every subsequent statement fail.
func (db *DB) isPredefinedRecord(ctx context.Context, table string, id uuid.UUID) (bool, error) {
	exists, err := db.dialect.ColumnExists(ctx, db, table, "_is_predefined")
	if err != nil {
		return false, fmt.Errorf("check %s._is_predefined: %w", table, err)
	}
	if !exists {
		return false, nil
	}

	var isPredefined bool
	err = db.QueryRow(ctx,
		fmt.Sprintf("SELECT _is_predefined FROM %s WHERE id = %s", table, db.dialect.Placeholder(1)),
		idArg(db.dialect, id),
	).Scan(&isPredefined)
	if IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s._is_predefined: %w", table, err)
	}
	return isPredefined, nil
}

// SyncAllPredefined синхронизирует predefined-элементы всех справочников в
// порядке зависимостей (orderByDependency): справочник, на predefined которого
// ссылаются cross-ref поля, синхронизируется раньше ссылающегося — иначе
// GetPredefinedID не нашёл бы целевую запись.
func (db *DB) SyncAllPredefined(ctx context.Context, entities []*metadata.Entity) error {
	for _, e := range orderByDependency(entities) {
		if err := db.SyncPredefined(ctx, e); err != nil {
			return fmt.Errorf("sync predefined %s: %w", e.Name, err)
		}
	}
	return nil
}

// SyncPredefined upserts all predefined items declared in the entity YAML into
// the database. The _predefined_name is used as the conflict target so the UUID
// never changes on subsequent syncs — only field values are updated.
//
// Поддерживаются ссылки между predefined-элементами:
//   - self-ref — на predefined ТОГО ЖЕ справочника (Розничная.БазовыйТип:
//     Закупочная): резолвятся через локальную карту nameToUUID;
//   - cross-ref — на predefined ДРУГОГО справочника (Склад.Организация:
//     ГоловнойОфис): резолвятся через GetPredefinedID. Корректность порядка
//     обеспечивает SyncAllPredefined.
//
// Алгоритм:
//  1. Пре-аллоцировать UUID для каждого item (переиспользуя из БД при upsert).
//  2. Топологическая сортировка по self-reference полям.
//  3. INSERT в порядке зависимостей, заменяя имя на UUID для ref-полей.
//
// При цикле в self-ref зависимостях возвращается ошибка.
func (db *DB) SyncPredefined(ctx context.Context, e *metadata.Entity) error {
	if len(e.Predefined) == 0 {
		return nil
	}
	d := db.dialect
	boolTrue := "TRUE"
	if d.Name() == "sqlite" {
		boolTrue = "1"
	}
	table := metadata.TableName(e.Name)

	// Шаг 1: для каждого predefined — собираем UUID. Если в БД уже есть
	// запись с тем же _predefined_name — берём её UUID, иначе генерим.
	nameToUUID := make(map[string]uuid.UUID, len(e.Predefined))
	for _, item := range e.Predefined {
		var idStr string
		err := db.QueryRow(ctx, fmt.Sprintf(
			`SELECT id FROM %s WHERE _predefined_name = %s AND _is_predefined = %s LIMIT 1`,
			table, d.Placeholder(1), boolTrue),
			item.Name,
		).Scan(&idStr)
		if err == nil {
			if id, err := uuid.Parse(idStr); err == nil {
				nameToUUID[item.Name] = id
				continue
			}
		}
		nameToUUID[item.Name] = uuid.New()
	}

	// Шаг 2: топологическая сортировка. Граф ориентирован: item → items на
	// которые он ссылается через self-ref поля. Вставляем в порядке
	// «зависимости первыми». Карта имён → item для быстрого lookup.
	byName := make(map[string]*metadata.PredefinedItem, len(e.Predefined))
	for _, it := range e.Predefined {
		byName[it.Name] = it
	}
	// Определяем self-ref поля.
	selfRefFields := make(map[string]bool)
	for _, f := range e.Fields {
		if f.RefEntity == e.Name {
			selfRefFields[strings.ToLower(f.Name)] = true
		}
	}
	ordered, err := topoSortPredefined(e.Predefined, selfRefFields)
	if err != nil {
		return fmt.Errorf("sync predefined %s: %w", e.Name, err)
	}

	// Шаг 3: вставка в порядке зависимостей.
	for _, item := range ordered {
		cols := []string{"id", "_predefined_name", "_is_predefined"}
		phs := []string{d.Placeholder(1), d.Placeholder(2), boolTrue}
		args := []any{idArg(d, nameToUUID[item.Name]), item.Name}
		updates := []string{"_is_predefined = " + boolTrue}
		argIdx := 3

		for _, f := range e.Fields {
			col := metadata.ColumnName(f)
			val, ok := item.Fields[f.Name]
			if !ok {
				continue
			}
			// Self-ref поле: значение — имя другого predefined ТОГО ЖЕ
			// справочника, подменяем на его UUID из локальной карты.
			if selfRefFields[strings.ToLower(f.Name)] {
				if refName, ok := val.(string); ok {
					if refUUID, found := nameToUUID[refName]; found {
						val = idArg(d, refUUID)
					}
				}
			} else if f.RefEntity != "" && f.RefEntity != e.Name {
				// Cross-ref (#14): значение — имя predefined ДРУГОГО
				// справочника. Уже-UUID оставляем как есть; иначе трактуем
				// как имя предопределённого и резолвим. Целевой справочник
				// синхронизирован раньше (см. SyncAllPredefined).
				if refName, ok := val.(string); ok && refName != "" {
					if _, err := uuid.Parse(refName); err != nil {
						refUUID, perr := db.GetPredefinedID(ctx, f.RefEntity, refName)
						if perr != nil {
							return fmt.Errorf(
								"sync predefined %s.%s: поле %q ссылается на предопределённый %s.%s — не найден: %w",
								e.Name, item.Name, f.Name, f.RefEntity, refName, perr)
						}
						val = idArg(d, refUUID)
					}
				}
			}
			cols = append(cols, col)
			phs = append(phs, d.Placeholder(argIdx))
			args = append(args, val)
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
			argIdx++
		}

		sql := fmt.Sprintf(
			`INSERT INTO %s (%s) VALUES (%s)
			 ON CONFLICT (_predefined_name) WHERE _is_predefined = %s
			 DO UPDATE SET %s`,
			table,
			strings.Join(cols, ", "),
			strings.Join(phs, ", "),
			boolTrue,
			strings.Join(updates, ", "),
		)
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("sync predefined %s.%s: %w", e.Name, item.Name, err)
		}
	}
	return nil
}

// topoSortPredefined сортирует predefined-элементы по self-reference
// зависимостям. Если A.SelfRefField = B, то B вставляется раньше A.
// При обнаружении цикла возвращает ошибку с указанием участников.
func topoSortPredefined(items []*metadata.PredefinedItem, selfRefFields map[string]bool) ([]*metadata.PredefinedItem, error) {
	byName := make(map[string]*metadata.PredefinedItem, len(items))
	for _, it := range items {
		byName[it.Name] = it
	}

	// deps[name] = список имён, от которых зависит name.
	deps := make(map[string][]string, len(items))
	for _, it := range items {
		var d []string
		for fieldName, val := range it.Fields {
			if !selfRefFields[strings.ToLower(fieldName)] {
				continue
			}
			refName, ok := val.(string)
			if !ok || refName == "" {
				continue
			}
			if _, exists := byName[refName]; exists {
				d = append(d, refName)
			}
		}
		deps[it.Name] = d
	}

	// DFS с тремя цветами.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(items))
	var out []*metadata.PredefinedItem
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch color[name] {
		case black:
			return nil
		case gray:
			return i18nerr.Errorf("цикл в self-reference predefined: %s → %s",
				strings.Join(path, " → "), name)
		}
		color[name] = gray
		for _, dep := range deps[name] {
			nextPath := append(append([]string(nil), path...), name)
			if err := visit(dep, nextPath); err != nil {
				return err
			}
		}
		color[name] = black
		out = append(out, byName[name])
		return nil
	}
	for _, it := range items {
		if err := visit(it.Name, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetPredefinedID returns the UUID of a predefined item by its name.
func (db *DB) GetPredefinedID(ctx context.Context, entityName, predefinedName string) (uuid.UUID, error) {
	d := db.dialect
	boolTrue := "TRUE"
	if d.Name() == "sqlite" {
		boolTrue = "1"
	}
	table := metadata.TableName(entityName)
	var idStr string
	err := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT id FROM %s WHERE _predefined_name = %s AND _is_predefined = %s`,
			table, d.Placeholder(1), boolTrue),
		predefinedName,
	).Scan(&idStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("predefined %s.%s not found: %w", entityName, predefinedName, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("predefined %s.%s: bad uuid: %w", entityName, predefinedName, err)
	}
	return id, nil
}

// GetPredefinedIDStr is a string-returning variant of GetPredefinedID for the
// DSL interpreter interface (avoids uuid dependency in the interpreter package).
func (db *DB) GetPredefinedIDStr(ctx context.Context, entityName, predefinedName string) (string, error) {
	id, err := db.GetPredefinedID(ctx, entityName, predefinedName)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// FindCatalogByField looks up a single catalog row by exact match of fieldName.
// Returns (id, displayValue, true) on hit; ("", "", false, nil) when not found.
// displayValue is the matched field's value — handy for building a *Ref.
// fieldName lookup is case-insensitive against entity.Fields names.
func (db *DB) FindCatalogByField(ctx context.Context, entity *metadata.Entity, fieldName, value string) (string, string, bool, error) {
	var field *metadata.Field
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, fieldName) {
			field = &entity.Fields[i]
			break
		}
	}
	if field == nil {
		return "", "", false, fmt.Errorf("entity %s has no field %q", entity.Name, fieldName)
	}
	col := metadata.ColumnName(*field)
	table := metadata.TableName(entity.Name)
	d := db.dialect
	rows, err := db.Query(ctx,
		fmt.Sprintf(`SELECT id, %s FROM %s WHERE %s = %s LIMIT 1`, col, table, col, d.Placeholder(1)),
		value,
	)
	if err != nil {
		return "", "", false, fmt.Errorf("find %s.%s: %w", entity.Name, fieldName, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", "", false, nil
	}
	var idStr, display string
	if err := rows.Scan(&idStr, &display); err != nil {
		return "", "", false, fmt.Errorf("find %s.%s scan: %w", entity.Name, fieldName, err)
	}
	return idStr, display, true, nil
}

// ListCatalogMatchesByField returns every matching row in deterministic order.
// It is used when row-level access is active: callers must check each row
// before deciding whether the result is absent, unique, or ambiguous. Returning
// only LIMIT 1 or COUNT(*) would either hide a later visible row or disclose the
// number of rows that the current user cannot read.
func (db *DB) ListCatalogMatchesByField(ctx context.Context, entity *metadata.Entity, fieldName, value string) ([]string, []string, error) {
	var field *metadata.Field
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, fieldName) {
			field = &entity.Fields[i]
			break
		}
	}
	if field == nil {
		return nil, nil, fmt.Errorf("entity %s has no field %q", entity.Name, fieldName)
	}
	col := metadata.ColumnName(*field)
	table := metadata.TableName(entity.Name)
	rows, err := db.Query(ctx,
		fmt.Sprintf(`SELECT id, %s FROM %s WHERE %s = %s ORDER BY id`, col, table, col, db.dialect.Placeholder(1)),
		value,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list matches %s.%s: %w", entity.Name, fieldName, err)
	}
	defer rows.Close()
	var ids, displays []string
	for rows.Next() {
		var id, display string
		if err := rows.Scan(&id, &display); err != nil {
			return nil, nil, fmt.Errorf("list matches %s.%s scan: %w", entity.Name, fieldName, err)
		}
		ids = append(ids, id)
		displays = append(displays, display)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list matches %s.%s rows: %w", entity.Name, fieldName, err)
	}
	return ids, displays, nil
}

// MatchCatalogByField ищет записи справочника/документа по точному совпадению
// реквизита для сценария safe-match (0 / 1 / несколько). Возвращает количество
// совпадений и — только когда оно ровно одно — id и представление найденной
// записи. Количество всегда точное (чтобы прикладной код сообщал число дублей):
// один запрос, где LIMIT 1 берёт id/представление, а вложенный COUNT(*) считает
// все совпадения (1 round-trip и 1 сканирование вместо прежних двух).
// fieldName сопоставляется без учёта регистра.
func (db *DB) MatchCatalogByField(ctx context.Context, entity *metadata.Entity, fieldName, value string) (string, string, int, error) {
	var field *metadata.Field
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, fieldName) {
			field = &entity.Fields[i]
			break
		}
	}
	if field == nil {
		return "", "", 0, fmt.Errorf("entity %s has no field %q", entity.Name, fieldName)
	}
	col := metadata.ColumnName(*field)
	table := metadata.TableName(entity.Name)
	d := db.dialect
	// Один запрос: LIMIT 1 берёт id/представление первой записи, а вложенный
	// COUNT(*) считает ВСЕ совпадения (точное число для отчёта о дублях). 0
	// совпадений — основная выборка пуста (rows.Next()==false), COUNT не нужен.
	// Два плейсхолдера (а не повтор одного) — универсально для PostgreSQL ($1/$2)
	// и SQLite (?, ?).
	rows, err := db.Query(ctx,
		fmt.Sprintf(`SELECT id, %s, (SELECT COUNT(*) FROM %s WHERE %s = %s) FROM %s WHERE %s = %s LIMIT 1`,
			col, table, col, d.Placeholder(1), table, col, d.Placeholder(2)),
		value, value,
	)
	if err != nil {
		return "", "", 0, fmt.Errorf("match %s.%s: %w", entity.Name, fieldName, err)
	}
	if !rows.Next() {
		rows.Close()
		return "", "", 0, nil // 0 совпадений
	}
	var idStr, display string
	cnt := 0
	if err := rows.Scan(&idStr, &display, &cnt); err != nil {
		rows.Close()
		return "", "", 0, fmt.Errorf("match %s.%s scan: %w", entity.Name, fieldName, err)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", "", 0, fmt.Errorf("match %s.%s rows: %w", entity.Name, fieldName, err)
	}
	rows.Close()
	if cnt == 1 {
		return idStr, display, 1, nil
	}
	return "", "", cnt, nil // cnt > 1 — несколько; точное число, id не отдаём (неоднозначно)
}

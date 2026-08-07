package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// refUUIDGetter is implemented by DSL *Ref values — extracts UUID without
// importing the interpreter package (would be a circular dependency).
type refUUIDGetter interface{ GetRefUUID() string }

// resolveRefArg converts a reference field value to a DB-ready UUID argument.
// Accepts string (UUID or empty), or any type implementing GetRefUUID().
func resolveRefArg(d Dialect, v any) any {
	switch val := v.(type) {
	case string:
		if val != "" {
			if id, err := uuid.Parse(val); err == nil {
				return idArg(d, id)
			}
		}
	case refUUIDGetter:
		if uuidStr := val.GetRefUUID(); uuidStr != "" {
			if id, err := uuid.Parse(uuidStr); err == nil {
				return idArg(d, id)
			}
		}
	}
	return v
}

// refStringer — *Ref имеет String() через runtime.Object интерфейс. Здесь
// избегаем импорт interpreter (был бы циклический), поэтому ловим
// через fmt.Stringer.
type refStringer interface{ String() string }

// normalizeRegArg — общая нормализация значения перед записью в любую
// колонку регистра ( Если значение — *Ref:
//   - reference-поле  → UUID (через resolveRefArg)
//   - не-ref поле     → строка из String() (имя записи)
//
// Для не-Ref значений возвращается как есть.
func normalizeRegArg(d Dialect, v any, isRef bool) any {
	if isRef {
		return resolveRefArg(d, v)
	}
	// Поле объявлено как string/number/etc., но DSL положил Ref —
	// исторический сценарий (П.17): измерение объявлено string как
	// workaround. Падали на pgx «unsupported type interpreter.Ref,
	// a struct». Кладём display-имя.
	if r, ok := v.(refUUIDGetter); ok {
		if s, ok2 := v.(refStringer); ok2 {
			return s.String()
		}
		return r.GetRefUUID()
	}
	return v
}

// OrphanStat describes orphaned movements (recorder document no longer exists).
type OrphanStat struct {
	RegisterName string
	RecorderType string
	Count        int
	// UnknownType — документа с таким именем нет в ТЕКУЩЕЙ конфигурации.
	//
	// Это принципиально другой случай, чем «документ удалён». Тип регистратора
	// хранится в движении строкой, поэтому он перестаёт совпадать с
	// конфигурацией сразу в нескольких безобидных ситуациях: документ
	// переименовали, команду запустили против другой (или частичной)
	// конфигурации, объект временно убрали. Движения при этом целы, и удалять
	// их нельзя: данные не должны исчезать оттого, что метаданные о них не
	// упоминают.
	UnknownType bool
}

// OrphanMovements returns stats about movements whose recorder document no longer exists.
//
// Ошибку запроса по регистру возвращаем наверх, а не проглатываем: иначе
// нечитаемый регистр (блокировка, обрыв соединения) выпадал бы из статистики, и
// doctor отчитался бы «осиротевших движений нет» не потому что их нет, а потому
// что не смотрели. Отсутствие таблицы — законный случай (регистр ещё не
// мигрирован), его отличаем явной проверкой HasTable и пропускаем (#622).
func (db *DB) OrphanMovements(ctx context.Context, registers []*metadata.Register, entities []*metadata.Entity) ([]OrphanStat, error) {
	d := db.dialect
	entityTable := make(map[string]string, len(entities))
	for _, e := range entities {
		entityTable[strings.ToLower(e.Name)] = metadata.TableName(e.Name)
	}
	var stats []OrphanStat
	for _, reg := range registers {
		table := metadata.RegisterTableName(reg.Name)
		if !db.HasTable(ctx, table) {
			continue // регистр ещё не мигрирован — проверять нечего
		}
		// Сначала полностью считываем типы регистраторов и закрываем курсор.
		// Вложенный QueryRow ниже нельзя выполнять при открытом rows: на
		// единственном SQLite-соединении (SetMaxOpenConns(1)) он зависнет,
		// ожидая соединение, которое держит незакрытый внешний курсор.
		rows, err := db.Query(ctx, fmt.Sprintf(
			"SELECT recorder_type, COUNT(*) FROM %s GROUP BY recorder_type", table))
		if err != nil {
			return nil, fmt.Errorf("%s: чтение движений: %w", reg.Name, err)
		}
		type recTotal struct {
			recType string
			total   int
		}
		var recTypes []recTotal
		for rows.Next() {
			var rt recTotal
			// Сбойную строку пропускаем: нулевой recTotal дал бы статистику
			// по несуществующему типу регистратора.
			if err := rows.Scan(&rt.recType, &rt.total); err != nil {
				continue
			}
			recTypes = append(recTypes, rt)
		}
		rows.Close()

		for _, rt := range recTypes {
			// Опорные движения свёртки (план 74) — не сироты: у них нет
			// документа-регистратора по замыслу. Пропускаем, иначе «Очистка
			// регистров» предложила бы удалить опорные остатки.
			if rt.recType == RollupRecorderType {
				continue
			}
			tbl, exists := entityTable[strings.ToLower(rt.recType)]
			var count int
			if !exists {
				count = rt.total
			} else {
				// Не смогли посчитать — не сообщаем о сиротах: лучше
				// недосказать, чем предложить удалить неизвестно что.
				if err := db.QueryRow(ctx, fmt.Sprintf(
					"SELECT COUNT(*) FROM %s WHERE recorder_type = %s AND recorder NOT IN (SELECT id FROM %s)",
					table, d.Placeholder(1), tbl), rt.recType).Scan(&count); err != nil {
					continue
				}
			}
			if count > 0 {
				stats = append(stats, OrphanStat{
					RegisterName: reg.Name, RecorderType: rt.recType,
					Count: count, UnknownType: !exists,
				})
			}
		}
	}
	return stats, nil
}

// DeleteOrphanMovements удаляет движения, чей документ-регистратор не найден.
//
// Удаляются ТОЛЬКО движения документов, известных конфигурации: тип есть в
// метаданных, а документа с таким id в таблице нет. Движения с неизвестным
// типом регистратора не трогаются — см. OrphanStat.UnknownType: неизвестный
// тип означает расхождение конфигурации с данными (документ переименовали,
// запустились против другой конфигурации), а не удалённый документ. Раньше они
// удалялись безусловным DELETE по типу, то есть переименование документа
// стирало всю его историю движений.
//
// Для тех, кто действительно хочет удалить движения выбывшего документа, есть
// DeleteMovementsOfUnknownRecorderType — отдельный, явный вызов.
func (db *DB) DeleteOrphanMovements(ctx context.Context, registers []*metadata.Register, entities []*metadata.Entity) int64 {
	entityTable := make(map[string]string, len(entities))
	for _, e := range entities {
		entityTable[strings.ToLower(e.Name)] = metadata.TableName(e.Name)
	}
	var total int64
	for _, reg := range registers {
		table := metadata.RegisterTableName(reg.Name)
		rows, err := db.Query(ctx, fmt.Sprintf(
			"SELECT DISTINCT recorder_type FROM %s", table))
		if err != nil {
			continue
		}
		var types []string
		for rows.Next() {
			var t string
			// КРИТИЧНО: при сбое Scan строка t осталась бы пустой, и ниже
			// выполнился бы DELETE ... WHERE recorder_type = '' — удаление,
			// вызванное непрочитанной строкой. Пропускаем.
			if err := rows.Scan(&t); err != nil {
				continue
			}
			types = append(types, t)
		}
		rows.Close()

		d := db.dialect
		for _, recType := range types {
			if recType == RollupRecorderType {
				continue // опорные движения свёртки — не сироты (план 74)
			}
			tbl, exists := entityTable[strings.ToLower(recType)]
			if !exists {
				// Документа нет в конфигурации — движения не наши, чтобы их
				// удалять (см. комментарий к функции).
				continue
			}
			sql := fmt.Sprintf(
				"DELETE FROM %s WHERE recorder_type = %s AND recorder NOT IN (SELECT id FROM %s)",
				table, d.Placeholder(1), tbl)
			if ct, err := db.Exec(ctx, sql, recType); err == nil {
				total += ct.RowsAffected
			}
		}
	}
	return total
}

// DeleteMovementsOfUnknownRecorderType удаляет движения, тип регистратора
// которых отсутствует в конфигурации, — по конкретному типу, названному
// вызывающим.
//
// Отдельная функция и обязательный список типов — намеренно. Это операция «я
// знаю, что документ ИмяX убран навсегда, снеси его историю»: угадывать такое
// по расхождению метаданных с данными нельзя, потому что то же расхождение
// даёт обычное переименование.
func (db *DB) DeleteMovementsOfUnknownRecorderType(
	ctx context.Context, registers []*metadata.Register, recorderTypes []string,
) int64 {
	wanted := wantedRecorderTypes(recorderTypes)
	if len(wanted) == 0 {
		return 0
	}
	d := db.dialect
	var total int64
	for _, reg := range registers {
		table := metadata.RegisterTableName(reg.Name)
		for t := range wanted {
			ct, err := db.Exec(ctx, fmt.Sprintf(
				"DELETE FROM %s WHERE recorder_type = %s", table, d.Placeholder(1)), t)
			if err == nil {
				total += ct.RowsAffected
			}
		}
	}
	return total
}

// CountMovementsOfRecorderType считает движения по названным типам регистратора
// во всех регистрах, ничего не меняя, — сухой прогон для --forget-document:
// удаление необратимо, поэтому объём стоит увидеть заранее.
func (db *DB) CountMovementsOfRecorderType(
	ctx context.Context, registers []*metadata.Register, recorderTypes []string,
) int64 {
	wanted := wantedRecorderTypes(recorderTypes)
	if len(wanted) == 0 {
		return 0
	}
	d := db.dialect
	var total int64
	for _, reg := range registers {
		table := metadata.RegisterTableName(reg.Name)
		for t := range wanted {
			var n int64
			err := db.QueryRow(ctx, fmt.Sprintf(
				"SELECT COUNT(*) FROM %s WHERE recorder_type = %s", table, d.Placeholder(1)), t).Scan(&n)
			if err == nil {
				total += n
			}
		}
	}
	return total
}

// wantedRecorderTypes нормализует список типов регистратора для forget-операций.
//
// Сравнение точное, без LOWER(): у SQLite встроенный LOWER() работает только с
// латиницей, поэтому «Реализация» он не приводит ни к чему, и регистронезависимое
// сравнение молча не нашло бы ни одной строки. Тип приходит из отчёта проверки —
// ровно в том виде, в каком лежит в данных, — так что точного совпадения
// достаточно. Пустой тип и опорные движения свёртки не трогаем никогда.
func wantedRecorderTypes(recorderTypes []string) map[string]bool {
	wanted := make(map[string]bool, len(recorderTypes))
	for _, t := range recorderTypes {
		t = strings.TrimSpace(t)
		if t == "" || t == RollupRecorderType {
			continue
		}
		wanted[t] = true
	}
	return wanted
}

// WriteMovements replaces all movements for a document in the given register.
func (db *DB) WriteMovements(ctx context.Context, regName, recorderType string, recorderID uuid.UUID, rows []map[string]any, reg *metadata.Register, period *time.Time) error {
	return db.WithTxIfNeeded(ctx, func(ctx context.Context) error {
		if reg.TotalsUsable() {
			if err := db.AdvisoryXactLock(ctx, []string{"register-totals|" + strings.ToLower(reg.Name)}); err != nil {
				return err
			}
		}
		return db.writeMovementsInTx(ctx, regName, recorderType, recorderID, rows, reg, period)
	})
}

func (db *DB) writeMovementsInTx(ctx context.Context, regName, recorderType string, recorderID uuid.UUID, rows []map[string]any, reg *metadata.Register, period *time.Time) error {
	d := db.dialect
	table := metadata.RegisterTableName(regName)

	// План 80: до удаления движений снимаем кортежи измерений регистратора —
	// после записи их итоги пересчитываются (в этой же транзакции). Так строка
	// итогов затронутого набора измерений остаётся согласованной с движениями.
	var oldTuples [][]any
	if reg.TotalsUsable() {
		var err error
		if oldTuples, err = db.distinctDimTuples(ctx, reg, recorderType, idArg(d, recorderID)); err != nil {
			return fmt.Errorf("totals %s: capture old tuples: %w", regName, err)
		}
	}

	if err := db.exec(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE recorder = %s AND recorder_type = %s",
			table, d.Placeholder(1), d.Placeholder(2)),
		idArg(d, recorderID), recorderType,
	); err != nil {
		return fmt.Errorf("clear movements %s: %w", regName, err)
	}

	for i, row := range rows {
		vidDvizh := fmt.Sprintf("%v", ciGet(row, "ВидДвижения"))
		if vidDvizh == "" || vidDvizh == "<nil>" {
			vidDvizh = "Приход"
		}
		cols := []string{"id", "recorder", "recorder_type", "line_number", "period", "вид_движения"}
		phs := []string{d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6)}
		periodVal := any(time.Now())
		if period != nil {
			periodVal = *period
		}
		args := []any{idArg(d, uuid.New()), idArg(d, recorderID), recorderType, i + 1, periodVal, vidDvizh}
		idx := 7

		allFields := append(append([]metadata.Field{}, reg.Dimensions...), append(reg.Resources, reg.Attributes...)...)
		for _, f := range allFields {
			cols = append(cols, metadata.ColumnName(f))
			phs = append(phs, d.Placeholder(idx))
			v := ciGet(row, f.Name)
			v = normalizeRegArg(d, v, f.RefEntity != "")
			args = append(args, v)
			idx++
		}

		sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(phs, ", "))
		if err := db.exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("write movement %s row %d: %w", regName, i+1, err)
		}
	}

	if reg.TotalsUsable() {
		if err := db.updateTotalsForRecorder(ctx, reg, recorderType, idArg(d, recorderID), oldTuples); err != nil {
			return fmt.Errorf("update totals %s: %w", regName, err)
		}
	}
	return nil
}

// GetMovements returns movement rows for a register, ordered by period and
// recorder. Если f задан — выборка сужается по измерениям и периоду (issue #45).
func (db *DB) GetMovements(ctx context.Context, regName string, reg *metadata.Register, f RegFilter) ([]map[string]any, error) {
	table := metadata.RegisterTableName(regName)
	cols := []string{"recorder", "recorder_type", "line_number", "period", "вид_движения"}
	allFields := append(append([]metadata.Field{}, reg.Dimensions...), append(reg.Resources, reg.Attributes...)...)
	for _, f := range allFields {
		cols = append(cols, metadata.ColumnName(f))
	}
	where, args := dimWhereClause(db.dialect, reg.Dimensions, f, 1, true, true)
	whereParts := make([]string, 0, 2)
	if where != "" {
		whereParts = append(whereParts, where)
	}
	if cond, condArgs, _, err := PredicateSQL(db.dialect, RegisterPredicateEntity(reg), f.RowFilter, len(args)+1); err != nil {
		return nil, fmt.Errorf("get movements %s row filter: %w", regName, err)
	} else if cond != "" {
		whereParts = append(whereParts, cond)
		args = append(args, condArgs...)
	}
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), table)
	if len(whereParts) > 0 {
		query += " WHERE " + strings.Join(whereParts, " AND ")
	}
	query += " ORDER BY period, recorder, line_number"
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get movements %s: %w", regName, err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		row["recorder"] = normalizeValue(dest[0])
		row["recorder_type"] = dest[1]
		row["line_number"] = dest[2]
		if dest[3] != nil {
			if t, ok := dest[3].(time.Time); ok {
				row["period"] = t.Format("02.01.2006")
			} else {
				row["period"] = dest[3]
			}
		}
		row["вид_движения"] = dest[4]
		for i, f := range allFields {
			row[f.Name] = normalizeFieldValue(f, dest[5+i])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// GetDocumentMovements returns all register movements for a specific document.
// Returns a map keyed by register name, each value is a slice of movement rows.
func (db *DB) GetDocumentMovements(ctx context.Context, recorderID uuid.UUID, registers []*metadata.Register) (map[string][]map[string]any, error) {
	result := make(map[string][]map[string]any)
	for _, reg := range registers {
		table := metadata.RegisterTableName(reg.Name)
		cols := []string{"line_number", "period", "вид_движения"}
		allFields := append(append([]metadata.Field{}, reg.Dimensions...), append(reg.Resources, reg.Attributes...)...)
		for _, f := range allFields {
			cols = append(cols, metadata.ColumnName(f))
		}
		query := fmt.Sprintf("SELECT %s FROM %s WHERE recorder = %s ORDER BY line_number",
			strings.Join(cols, ", "), table, db.dialect.Placeholder(1))
		rows, err := db.Query(ctx, query, idArg(db.dialect, recorderID))
		if err != nil {
			continue
		}
		var regRows []map[string]any
		for rows.Next() {
			dest := make([]any, len(cols))
			ptrs := make([]any, len(dest))
			for i := range dest {
				ptrs[i] = &dest[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return result, err
			}
			row := make(map[string]any, len(cols))
			row["line_number"] = dest[0]
			if t, ok := dest[1].(time.Time); ok {
				row["period"] = t.Format("02.01.2006")
			} else {
				row["period"] = dest[1]
			}
			row["вид_движения"] = dest[2]
			for i, f := range allFields {
				row[f.Name] = normalizeFieldValue(f, dest[3+i])
			}
			regRows = append(regRows, row)
		}
		rows.Close()
		if len(regRows) > 0 {
			result[reg.Name] = regRows
		}
	}
	return result, nil
}

// GetBalances returns aggregated balances grouped by dimension fields.
// Если f задан — учитываются движения только по выбранным измерениям и до
// f.To включительно («остатки на дату»); f.From для остатков игнорируется (#45).
func (db *DB) GetBalances(ctx context.Context, regName string, reg *metadata.Register, f RegFilter) ([]map[string]any, error) {
	table := metadata.RegisterTableName(regName)

	var selectParts, groupBy []string
	var dimNames []string
	for _, f := range reg.Dimensions {
		col := metadata.ColumnName(f)
		selectParts = append(selectParts, col)
		groupBy = append(groupBy, col)
		dimNames = append(dimNames, f.Name)
	}
	var resNames []string
	for _, f := range reg.Resources {
		col := metadata.ColumnName(f)
		selectParts = append(selectParts, fmt.Sprintf(
			"SUM(CASE WHEN вид_движения = 'Приход' THEN %s ELSE -%s END) AS %s", col, col, col))
		resNames = append(resNames, f.Name)
	}

	where, args := dimWhereClause(db.dialect, reg.Dimensions, f, 1, false /*from игнорируем*/, true)
	whereParts := make([]string, 0, 2)
	if where != "" {
		whereParts = append(whereParts, where)
	}
	if cond, condArgs, _, err := PredicateSQL(db.dialect, RegisterPredicateEntity(reg), f.RowFilter, len(args)+1); err != nil {
		return nil, fmt.Errorf("get balances %s row filter: %w", regName, err)
	} else if cond != "" {
		whereParts = append(whereParts, cond)
		args = append(args, condArgs...)
	}
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selectParts, ", "), table)
	if len(whereParts) > 0 {
		query += " WHERE " + strings.Join(whereParts, " AND ")
	}
	if len(groupBy) > 0 {
		query += " GROUP BY " + strings.Join(groupBy, ", ")
		query += " ORDER BY " + strings.Join(groupBy, ", ")
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get balances %s: %w", regName, err)
	}
	defer rows.Close()

	totalCols := len(reg.Dimensions) + len(reg.Resources)
	var result []map[string]any
	for rows.Next() {
		dest := make([]any, totalCols)
		ptrs := make([]any, totalCols)
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, totalCols)
		for i, name := range dimNames {
			row[name] = normalizeValue(dest[i])
		}
		for i, name := range resNames {
			row[name] = normalizeNumber(dest[len(reg.Dimensions)+i])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ciGet does a case-insensitive map lookup (DSL stores keys in lowercase).
func ciGet(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	low := strings.ToLower(key)
	for k, v := range m {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

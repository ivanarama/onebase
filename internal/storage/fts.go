package storage

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
)

// Полнотекстовый (глобальный) поиск — план 82.
//
// Схема одна на оба диалекта: обычная таблица _fts с текстом объекта, а
// диалект-специфичной остаётся только надстройка над ней — GIN по
// материализованному tsvector в PostgreSQL и внешний FTS5-индекс в SQLite.
// Благодаря этому запись индекса — один и тот же UPSERT, и обе СУБД
// обслуживает один код в crud.go; расходятся только DDL и SELECT поиска.
const (
	ftsTable    = "_fts"
	ftsIndexTbl = "_fts_idx" // FTS5 external content index (только SQLite)
	// ftsMaxTokens ограничивает длину поискового выражения: строка поиска —
	// пользовательский ввод, а каждый токен добавляет ветку в tsquery/MATCH.
	ftsMaxTokens = 16
	// ftsMaxBody ограничивает размер индексируемого текста одного объекта,
	// чтобы richtext-«простыня» не раздувала индекс.
	ftsMaxBody = 64 * 1024
)

// FTSDoc — текст одного объекта в полнотекстовом индексе.
type FTSDoc struct {
	Kind  string // catalog | document
	Name  string // имя сущности из метаданных
	ID    uuid.UUID
	Title string // представление: первый непустой индексируемый реквизит
	Body  string // остальные индексируемые реквизиты через пробел
}

// FTSHit — одно совпадение глобального поиска.
type FTSHit struct {
	Kind  string
	Name  string
	ID    uuid.UUID
	Title string
	// Rank — релевантность, больше = лучше. Значения несравнимы между
	// диалектами (ts_rank против bm25), поэтому годятся только для сортировки
	// внутри одной выдачи.
	Rank float64
}

// FTSQuery — параметры поиска.
type FTSQuery struct {
	Text string
	// Names ограничивает выдачу перечисленными сущностями. Через него
	// вызывающий накладывает объектный RBAC: чего нет в списке — не ищется.
	// Пустой список означает «без ограничения», поэтому вызывающая сторона
	// обязана сама решить, что делать с пользователем без прав (см. UI/REST).
	Names  []string
	Limit  int
	Offset int
}

// FullTextIndex — часть полнотекстового поиска, зависящая от диалекта.
// Наполнение индекса от диалекта не зависит и живёт в методах DB.
type FullTextIndex interface {
	// EnsureSchema создаёт надстройку над _fts (GIN-колонку или FTS5-индекс).
	EnsureSchema(ctx context.Context, db *DB) error
	// Search возвращает совпадения, отсортированные по убыванию релевантности.
	Search(ctx context.Context, db *DB, q FTSQuery) ([]FTSHit, error)
}

func (db *DB) fullTextIndex() FullTextIndex {
	if db.IsSQLite() {
		return sqliteFullText{}
	}
	return pgFullText{}
}

// Состояние схемы поиска в процессе: 0 — не проверяли, 1 — таблица есть,
// 2 — таблицы нет. Нужно, чтобы запись объектов в базу, ещё не прошедшую
// migrate, не падала на INSERT в несуществующий _fts (в PostgreSQL это
// вдобавок оборвало бы транзакцию записи целиком).
const (
	ftsStateUnknown int32 = 0
	ftsStateReady   int32 = 1
	ftsStateAbsent  int32 = 2
)

func (db *DB) ftsAvailable(ctx context.Context) bool {
	switch atomic.LoadInt32(&db.ftsState) {
	case ftsStateReady:
		return true
	case ftsStateAbsent:
		return false
	}
	exists, err := db.tableExists(ctx, ftsTable)
	if err != nil {
		// Пробу не считаем приговором: не кэшируем, попробуем в следующий раз.
		return false
	}
	state := ftsStateAbsent
	if exists {
		state = ftsStateReady
	}
	atomic.StoreInt32(&db.ftsState, state)
	return exists
}

func (db *DB) tableExists(ctx context.Context, table string) (bool, error) {
	var q string
	if db.IsSQLite() {
		q = "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type IN ('table','view') AND name = ?)"
	} else {
		q = "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)"
	}
	var exists bool
	if err := db.QueryRow(ctx, q, table).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// EnsureFullTextSchema создаёт таблицу полнотекстового индекса и надстройку
// над ней. Вызывается из Migrate; идемпотентна.
//
// Возвращает признак «схемы не было»: при обновлении платформы на существующей
// базе после этого нужен разовый backfill, иначе строка поиска в шапке есть, а
// находит ноль объектов — и узнать об этом можно только догадавшись выполнить
// `onebase reindex`.
func (db *DB) EnsureFullTextSchema(ctx context.Context) (needBackfill bool, err error) {
	d := db.dialect
	existed, err := db.tableExists(ctx, ftsTable)
	if err != nil {
		return false, fmt.Errorf("полнотекстовый индекс: проверка %s: %w", ftsTable, err)
	}
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			owner_kind TEXT NOT NULL,
			owner_name TEXT NOT NULL,
			owner_id   %s NOT NULL,
			title      TEXT NOT NULL DEFAULT '',
			body       TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (owner_name, owner_id)
		)`, ftsTable, d.TypeUUID())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return false, fmt.Errorf("полнотекстовый индекс: создание %s: %w", ftsTable, err)
	}
	// Индекс по owner_id: удаление из индекса ищет по одному owner_id
	// (DeleteFromFullTextIndex, в т.ч. изнутри upsert для объектов без
	// индексируемого содержимого — горячий путь записи). Первичный ключ
	// (owner_name, owner_id) для этого неприменим — ведущей owner_name в
	// предикате нет, и обе СУБД брали бы полный скан общего индекса, растущий
	// вместе со всей базой (#623). IF NOT EXISTS — индекс нужен и существующим
	// базам, поэтому здесь же, где CREATE TABLE.
	if _, err := db.Exec(ctx, fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s_owner_id ON %s (owner_id)", ftsTable, ftsTable)); err != nil {
		return false, fmt.Errorf("полнотекстовый индекс: индекс по owner_id: %w", err)
	}
	if err := db.fullTextIndex().EnsureSchema(ctx, db); err != nil {
		return false, err
	}
	atomic.StoreInt32(&db.ftsState, ftsStateReady)
	done, err := db.fullTextBackfillDone(ctx)
	if err != nil {
		return false, err
	}
	// Два независимых повода наполнить, и нужен любой из них.
	//
	// `!existed` — таблицы не было: свежая база или снесённый индекс.
	// `!done` — она была, но отметки о доведённом до конца наполнении нет.
	//
	// Одного «таблицы не было» не хватало (#615): создаётся она в начале
	// Migrate, наполняется в конце и вне транзакции, поэтому обрыв между этими
	// точками (Ctrl+C, падение, разрыв соединения) оставлял таблицу на месте
	// пустой. При следующем запуске она уже существовала, признак выходил
	// false, и наполнение не повторялось НИКОГДА — строка поиска работала и
	// молча не находила ничего. Понять это можно было, только догадавшись
	// выполнить `onebase reindex`.
	//
	// Одной отметки тоже не хватает: индекс могли уронить руками, а отметка
	// осталась бы. Поэтому проверяем оба сигнала, а не заменяем один другим.
	return !existed || !done, nil
}

// ftsBackfillDoneKey — отметка о том, что первичное наполнение индекса
// ДОВЕДЕНО ДО КОНЦА. Её ставит лишь успешно завершившаяся пересборка.
//
// Побочный эффект обновления: на базе, где индекс наполнен ещё старым кодом,
// отметки нет, и первый же migrate пересоберёт индекс заново. Это разовая
// работа и она идемпотентна, а альтернатива — оставить нечинёными как раз те
// базы, где наполнение когда-то оборвалось.
const ftsBackfillDoneKey = "fts_backfill_done"

func (db *DB) fullTextBackfillDone(ctx context.Context) (bool, error) {
	v, ok, err := db.GetSetting(ctx, ftsBackfillDoneKey)
	if err != nil {
		return false, fmt.Errorf("полнотекстовый индекс: чтение отметки о наполнении: %w", err)
	}
	return ok && v == "1", nil
}

func (db *DB) markFullTextBackfillDone(ctx context.Context) error {
	if err := db.SaveSetting(ctx, ftsBackfillDoneKey, "1"); err != nil {
		return fmt.Errorf("полнотекстовый индекс: отметка о наполнении: %w", err)
	}
	return nil
}

// IndexObject записывает объект в полнотекстовый индекс. Вызывается изнутри
// upsert — в той же транзакции, что и сама запись, поэтому откат записи
// откатывает и индекс. Объект без индексируемых реквизитов (`fulltext: []`)
// из индекса удаляется: конфигурация могла выключить поиск по нему.
func (db *DB) IndexObject(ctx context.Context, e *metadata.Entity, id uuid.UUID, fields map[string]any) error {
	if e == nil || !db.ftsAvailable(ctx) {
		return nil
	}
	doc := BuildFTSDoc(e, id, fields)
	if doc.Title == "" && doc.Body == "" {
		return db.DeleteFromFullTextIndex(ctx, e.Name, id)
	}
	return db.writeFTSDoc(ctx, doc)
}

func (db *DB) writeFTSDoc(ctx context.Context, doc FTSDoc) error {
	d := db.dialect
	sqlText := fmt.Sprintf(`
		INSERT INTO %s (owner_kind, owner_name, owner_id, title, body)
		VALUES (%s, %s, %s, %s, %s)
		ON CONFLICT (owner_name, owner_id) DO UPDATE
		SET owner_kind = EXCLUDED.owner_kind, title = EXCLUDED.title, body = EXCLUDED.body`,
		ftsTable, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5))
	if err := db.exec(ctx, sqlText, doc.Kind, doc.Name, idArg(d, doc.ID), doc.Title, doc.Body); err != nil {
		return fmt.Errorf("полнотекстовый индекс %s: %w", doc.Name, err)
	}
	return nil
}

// DeleteFromFullTextIndex убирает объект из индекса (удаление записи).
func (db *DB) DeleteFromFullTextIndex(ctx context.Context, entityName string, id uuid.UUID) error {
	if !db.ftsAvailable(ctx) {
		return nil
	}
	d := db.dialect
	// Удаляем по owner_id: идентификатор записи уникален сам по себе, а имя
	// объекта приходит от вызывающего в произвольном регистре (REST v1 берёт
	// его из URL), и строгое сравнение с каноническим owner_name оставляло
	// строку в индексе навсегда. Регистронезависимое сравнение в SQL тут не
	// помощник: lower() в SQLite умеет только ASCII, а имена русские.
	sqlText := fmt.Sprintf("DELETE FROM %s WHERE owner_id = %s", ftsTable, d.Placeholder(1))
	if err := db.exec(ctx, sqlText, idArg(d, id)); err != nil {
		return fmt.Errorf("полнотекстовый индекс %s: удаление: %w", entityName, err)
	}
	return nil
}

// SearchFullText выполняет глобальный поиск. Права здесь НЕ проверяются:
// вызывающий обязан передать в FTSQuery.Names список разрешённых объектов и
// отфильтровать строки политиками (планы 79/82) — см. ui/api.
func (db *DB) SearchFullText(ctx context.Context, q FTSQuery) ([]FTSHit, error) {
	if !db.ftsAvailable(ctx) {
		return nil, nil
	}
	if len(ftsTokens(q.Text)) == 0 {
		return nil, nil
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return db.fullTextIndex().Search(ctx, db, q)
}

// FTSRebuildStat — результат пересборки индекса по одному объекту.
type FTSRebuildStat struct {
	Entity  string
	Indexed int
}

// RebuildFullTextIndex пересобирает полнотекстовый индекс из данных базы
// (команда `onebase reindex`). Индекс наполняется инкрементально при записи,
// поэтому полный пересбор нужен после смены блока `fulltext:` в метаданных,
// после массовой загрузки мимо платформы или для починки расхождений.
//
// Чтение идёт постранично: база может не поместиться в память. Каждая пачка
// пишется своей транзакцией, поэтому длинная пересборка не держит одну
// транзакцию на всё время и не блокирует прикладную работу.
func (db *DB) RebuildFullTextIndex(ctx context.Context, entities []*metadata.Entity, batchSize int, progress func(FTSRebuildStat)) ([]FTSRebuildStat, error) {
	if _, err := db.EnsureFullTextSchema(ctx); err != nil {
		return nil, err
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	// Объекты, которых больше нет в конфигурации, оставили бы в выдаче ссылки
	// на несуществующие карточки — чистим их до пересборки.
	if err := db.deleteFTSRowsOutside(ctx, entities); err != nil {
		return nil, err
	}

	stats := make([]FTSRebuildStat, 0, len(entities))
	for _, e := range entities {
		n, err := db.rebuildEntityFTS(ctx, e, batchSize)
		if err != nil {
			return stats, err
		}
		stat := FTSRebuildStat{Entity: e.Name, Indexed: n}
		stats = append(stats, stat)
		if progress != nil {
			progress(stat)
		}
	}
	// Отметка ставится здесь, а не в Migrate: это единственная точка полной
	// пересборки (её зовут и migrate, и `onebase reindex`), и любой выход по
	// ошибке выше отметку не оставляет. Разложить это по вызывающим значило бы
	// снова получить инвариант, применённый в части мест.
	if err := db.markFullTextBackfillDone(ctx); err != nil {
		return stats, err
	}
	return stats, nil
}

func (db *DB) deleteFTSRowsOutside(ctx context.Context, entities []*metadata.Entity) error {
	d := db.dialect
	if len(entities) == 0 {
		return db.exec(ctx, "DELETE FROM "+ftsTable)
	}
	names := make([]any, 0, len(entities))
	phs := make([]string, 0, len(entities))
	for i, e := range entities {
		names = append(names, e.Name)
		phs = append(phs, d.Placeholder(i+1))
	}
	sqlText := fmt.Sprintf("DELETE FROM %s WHERE owner_name NOT IN (%s)", ftsTable, strings.Join(phs, ", "))
	return db.exec(ctx, sqlText, names...)
}

// ReindexEntityFullText пересобирает индекс одного объекта — точечный вариант
// RebuildFullTextIndex (`onebase reindex --entity`), не трогающий чужие строки.
func (db *DB) ReindexEntityFullText(ctx context.Context, e *metadata.Entity, batchSize int) (int, error) {
	if e == nil {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return db.rebuildEntityFTS(ctx, e, batchSize)
}

// rebuildEntityFTS пересобирает индекс одного объекта. Очистка и наполнение
// идут ОДНОЙ транзакцией: раздельными запросами любая ошибка в середине
// (пропала колонка, обрыв связи, Ctrl+C) оставляла объект вообще без индекса,
// и текст ошибки об этом не предупреждал.
func (db *DB) rebuildEntityFTS(ctx context.Context, e *metadata.Entity, batchSize int) (n int, err error) {
	txErr := db.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
		var innerErr error
		n, innerErr = db.rebuildEntityFTSTx(txCtx, e, batchSize)
		return innerErr
	})
	if txErr != nil {
		return 0, txErr
	}
	return n, nil
}

func (db *DB) rebuildEntityFTSTx(ctx context.Context, e *metadata.Entity, batchSize int) (int, error) {
	d := db.dialect
	fields := metadata.FullTextFields(e)
	// Сначала снимаем прежние строки объекта: без этого записи, выпавшие из
	// индекса (удалены мимо платформы, сузился список `fulltext`), остались бы
	// в выдаче навсегда.
	if err := db.exec(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE owner_name = %s", ftsTable, d.Placeholder(1)), e.Name); err != nil {
		return 0, fmt.Errorf("полнотекстовый индекс %s: очистка: %w", e.Name, err)
	}
	if len(fields) == 0 {
		return 0, nil
	}

	cols := []string{"id"}
	for _, f := range fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	table := metadata.TableName(e.Name)
	total := 0
	offset := 0
	for {
		// Порядок по id устойчив: LIMIT/OFFSET без ORDER BY может как
		// пропустить строку, так и выдать её дважды.
		q := fmt.Sprintf("SELECT %s FROM %s ORDER BY id LIMIT %s OFFSET %s",
			strings.Join(cols, ", "), table, d.Placeholder(1), d.Placeholder(2))
		docs, err := db.readFTSBatch(ctx, q, e, fields, batchSize, offset)
		if err != nil {
			return total, err
		}
		if len(docs) == 0 {
			return total, nil
		}
		if err := db.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
			for _, doc := range docs {
				if doc.Title == "" && doc.Body == "" {
					continue
				}
				if err := db.writeFTSDoc(txCtx, doc); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return total, err
		}
		total += len(docs)
		offset += len(docs)
		if len(docs) < batchSize {
			return total, nil
		}
	}
}

func (db *DB) readFTSBatch(ctx context.Context, q string, e *metadata.Entity, fields []metadata.Field, limit, offset int) ([]FTSDoc, error) {
	rows, err := db.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("полнотекстовый индекс %s: чтение: %w", e.Name, err)
	}
	defer rows.Close()
	var docs []FTSDoc
	for rows.Next() {
		dest := make([]any, len(fields)+1)
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("полнотекстовый индекс %s: чтение: %w", e.Name, err)
		}
		// normalizeValue приводит UUID к строке: pgx отдаёт его как [16]byte,
		// SQLite — как TEXT, и без нормализации %v дал бы байтовый мусор.
		id, err := uuid.Parse(fmt.Sprintf("%v", normalizeValue(dest[0])))
		if err != nil {
			return nil, fmt.Errorf("полнотекстовый индекс %s: id %v: %w", e.Name, dest[0], err)
		}
		values := make(map[string]any, len(fields))
		for i, f := range fields {
			values[f.Name] = dest[i+1]
		}
		docs = append(docs, BuildFTSDoc(e, id, values))
	}
	return docs, rows.Err()
}

// BuildFTSDoc собирает индексируемый текст объекта из значений реквизитов.
// Title показывается в выдаче поиска и весит больше в ранжировании, поэтому он
// выбирается тем же правилом, что и представление объекта в списках и пикерах —
// по ИМЕНИ реквизита (metadata.LabelFields), а не по позиции. Иначе у
// импортированной из 1С конфигурации в выдаче стоял бы код: конвертер кладёт
// «Код» первым (план 117, решение №3).
func BuildFTSDoc(e *metadata.Entity, id uuid.UUID, fields map[string]any) FTSDoc {
	doc := FTSDoc{Kind: string(e.Kind), Name: e.Name, ID: id}
	var parts []string
	for _, f := range ftsFieldsTitleFirst(e) {
		v := ftsNormalize(fieldTextValue(f, fields))
		if v == "" {
			continue
		}
		if doc.Title == "" {
			doc.Title = v
			continue
		}
		parts = append(parts, v)
	}
	doc.Body = truncateRunes(strings.Join(parts, " "), ftsMaxBody)
	doc.Title = truncateRunes(doc.Title, 1024)
	return doc
}

// fieldTextValue достаёт текст реквизита из map значений формы/записи.
// Ключи там встречаются и в исходном регистре имени, и в нижнем (DSL
// регистронезависим), поэтому пробуем оба — как fieldValueDialect.
// ftsFieldsTitleFirst — индексируемые реквизиты, но кандидат на Title вынесен
// вперёд. Состав индексируемого текста при этом не меняется: переставляется
// только порядок, а Title — это первое непустое значение.
func ftsFieldsTitleFirst(e *metadata.Entity) []metadata.Field {
	all := metadata.FullTextFields(e)
	label := metadata.LabelFields(e)
	if len(label) == 0 || len(all) == 0 {
		return all
	}
	out := make([]metadata.Field, 0, len(all))
	candidates := label
	if len(e.Presentation) == 0 {
		// Без явного ключа сохраняем прежнюю семантику: только основной label
		// выносится вперёд, остальные поля остаются в fulltext-порядке.
		candidates = label[:1]
	}
	seen := make(map[string]bool, len(candidates))
	// Все явные кандидаты идут первыми в объявленном порядке: BuildFTSDoc
	// возьмёт первый непустой и тем самым повторит fallback RowLabel.
	for _, candidate := range candidates {
		for _, f := range all {
			if strings.EqualFold(f.Name, candidate.Name) {
				out = append(out, f)
				seen[strings.ToLower(f.Name)] = true
				break
			}
		}
	}
	for _, f := range all {
		if !seen[strings.ToLower(f.Name)] {
			out = append(out, f)
		}
	}
	return out
}

func fieldTextValue(f metadata.Field, fields map[string]any) string {
	v, ok := fields[f.Name]
	if !ok || v == nil {
		v = fields[strings.ToLower(f.Name)]
	}
	if v == nil {
		return ""
	}
	var s string
	// Драйверы отдают текст и как string, и как []byte — второй случай через
	// %v превратился бы в список байтовых кодов.
	switch t := v.(type) {
	case string:
		s = t
	case []byte:
		s = string(t)
	default:
		s = fmt.Sprintf("%v", v)
	}
	if metadata.IsRichText(f.Type) {
		s = richtext.Plaintext(s)
	}
	return strings.TrimSpace(s)
}

func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// ftsNormalize заменяет любой знак, кроме буквы и цифры, пробелом: в индекс
// попадает тот же поток слов, на который ftsTokens режет поисковый запрос.
//
// Без этого движки расходятся. Разборщик PostgreSQL склеивает знак с числом:
// «РН-000012» даёт лексемы «рн» и «-000012», «+79990001122» — «+79990001122»,
// поэтому поиск по «000012» или по телефону без плюса ничего не находил, хотя
// на SQLite (unicode61 режет по не-буквам) находил. Регистр сохраняем: он всё
// равно сворачивается обоими движками, а title остаётся читаемым.
func ftsNormalize(s string) string {
	if s == "" {
		return ""
	}
	out := make([]rune, 0, len(s))
	space := true // подавляет ведущие и повторные пробелы
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, foldYo(r))
			space = false
			continue
		}
		if !space {
			out = append(out, ' ')
			space = true
		}
	}
	return strings.TrimRight(string(out), " ")
}

// foldYo сводит «ё» к «е». Ни один из движков этого не делает, а вводят обычно
// без точек: «артем» не находил «Артём», «королев» — «Королёв». Свёртка нужна
// одинаковая при индексации и при разборе запроса, иначе движки разъедутся.
func foldYo(r rune) rune {
	switch r {
	case 'ё':
		return 'е'
	case 'Ё':
		return 'Е'
	}
	return r
}

// ftsTokens режет пользовательский ввод на слова: остаются только буквы и
// цифры. Это же и есть защита от инъекции в синтаксис tsquery/MATCH — в
// поисковое выражение не попадает ни один служебный символ.
func ftsTokens(text string) []string {
	var (
		out   []string
		buf   strings.Builder
		flush = func() {
			if buf.Len() > 0 {
				out = append(out, strings.ToLower(buf.String()))
				buf.Reset()
			}
		}
	)
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(foldYo(r))
			continue
		}
		flush()
		if len(out) >= ftsMaxTokens {
			return out[:ftsMaxTokens]
		}
	}
	flush()
	if len(out) > ftsMaxTokens {
		out = out[:ftsMaxTokens]
	}
	return out
}

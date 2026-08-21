package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/jackc/pgx/v5/pgconn"
	sqlite "modernc.org/sqlite"
)

// Уникальность кода и номера (план 117E).
//
// Разрез — глобально по объекту: один код — один элемент справочника, как
// контроль уникальности в 1С. Базы после обмена не склеиваются, потому что
// префикс базы входит в сам код (план 117D).
//
// Две вещи, без которых уникальность была бы декоративной:
//
//  1. Пустой код пишется NULL, а NULL в уникальном индексе НЕ конфликтуют ни на
//     SQLite, ни на PostgreSQL. Поэтому «включили unique при половине пустых
//     кодов» прошло бы молча, а первый же дозаполненный дубль всплыл бы позже
//     и непонятно откуда. Отсюда проверка ДО создания индекса.
//  2. Сама СУБД сообщает о дубле кодом драйвера («UNIQUE constraint failed:
//     контрагенты.код» / SQLSTATE 23505). Показывать это пользователю, который
//     ввёл существующий код, нельзя — он не обязан знать про индексы.

// ErrCodeDuplicate — введено значение, которое уже занято. Сам по себе наружу
// не показывается: он опознаётся через errors.Is, а видит пользователь текст с
// объектом, полем и значением.
var ErrCodeDuplicate = errors.New("значение уже занято")

// RenumberHint — по этому фрагменту сообщения о пустых кодах лаунчер узнаёт
// класс ошибки «у отказа есть механическое лекарство» и предлагает кнопку
// вместо инструкции для консоли (#1067).
//
// Опознание по тексту, а не по типу ошибки, — не небрежность: ошибка пересекает
// границу процесса. `onebase run` падает дочерним процессом, лаунчер видит от
// него только хвост лога, и никакой errors.Is через эту границу не проходит.
// Имя команды переживает и перевод: Error() у i18nerr всегда рендерит по-русски,
// а во всех локалях этой строки «onebase renumber» остаётся как есть.
// Связь текста с маркером держит TestUniqueCode_PreconditionEmptyValuesMatrix.
const RenumberHint = "onebase renumber"

// duplicateError несёт локализуемое сообщение и при этом опознаётся через
// errors.Is(err, ErrCodeDuplicate). i18nerr.Wrapf для этого не годится: он
// дописал бы к тексту хвост «: значение уже занято» — пользователю, который и
// так прочитал «Код „К-000042“ уже занято другой записью», он ничего не
// добавляет.
type duplicateError struct{ msg error }

func (e duplicateError) Error() string        { return e.msg.Error() }
func (e duplicateError) Unwrap() error        { return e.msg }
func (e duplicateError) Is(target error) bool { return target == ErrCodeDuplicate }

// uniqueCodeIndexFields возвращает поле, на которое ставится уникальный индекс
// по numerator.unique. Пусто — уникальность не запрошена.
func uniqueCodeIndexField(e *metadata.Entity) string {
	if e == nil || e.Numerator == nil || !e.Numerator.Unique {
		return ""
	}
	return AutoNumberField(e)
}

// CodeStats — состояние кода (номера) объекта в базе: сколько записей без
// значения и сколько значений повторяется. Считается в том же разрезе, в
// котором работает нумератор: со scope: повтор внутри разреза — дубль, а между
// разрезами — нет.
type CodeStats struct {
	Field      string   // «Код» или «Номер»
	Empty      int      // записей без значения
	Duplicates int      // групп повторяющихся значений
	Examples   []string // до пяти повторяющихся значений
}

// CodeStats собирает статистику по коду/номеру. Второй результат — есть ли у
// объекта автонумеруемое поле вообще.
func (db *DB) CodeStats(ctx context.Context, e *metadata.Entity) (CodeStats, bool, error) {
	field := AutoNumberField(e)
	if field == "" {
		return CodeStats{}, false, nil
	}
	valueCol := columnOfField(e, field)
	if valueCol == "" {
		return CodeStats{}, false, nil
	}
	cols := []string{valueCol}
	if e.Numerator != nil && e.Numerator.Scope != "" {
		if c := columnOfField(e, e.Numerator.Scope); c != "" {
			cols = []string{c, valueCol}
		}
	}
	table := metadata.TableName(e.Name)
	st := CodeStats{Field: field}

	if err := db.QueryRow(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE %s IS NULL OR %s = ''", table, valueCol, valueCol)).Scan(&st.Empty); err != nil {
		return st, true, err
	}

	list := strings.Join(cols, ", ")
	rows, err := db.Query(ctx, fmt.Sprintf(
		"SELECT %s, COUNT(*) FROM %s WHERE %s IS NOT NULL AND %s <> '' GROUP BY %s HAVING COUNT(*) > 1",
		valueCol, table, valueCol, valueCol, list))
	if err != nil {
		return st, true, err
	}
	defer rows.Close()
	for rows.Next() {
		var value any
		var n int
		if err := rows.Scan(&value, &n); err != nil {
			return st, true, err
		}
		st.Duplicates++
		if len(st.Examples) < 5 {
			st.Examples = append(st.Examples, fmt.Sprintf("%v (%d)", value, n))
		}
	}
	return st, true, rows.Err()
}

// CheckCodeUniquePrecondition проверяет, можно ли включать уникальность: нет ли
// уже существующих дублей и пустых значений. Возвращает ошибку с человеческим
// текстом и подсказкой — что именно сделать.
//
// Разрез проверки тот же, что у будущего индекса (со scope: — разрез + код),
// иначе проверка запрещала бы то, что индекс разрешит, и наоборот.
func (db *DB) CheckCodeUniquePrecondition(ctx context.Context, e *metadata.Entity) error {
	if _, need := UniqueCodeIndexSpec(e); !need {
		return nil
	}
	st, ok, err := db.CodeStats(ctx, e)
	if err != nil || !ok {
		return nil // таблицы может ещё не быть — миграция создаст её сама
	}
	if st.Empty > 0 {
		return i18nerr.Errorf(
			"%s: уникальность %s включена, но у %d записей значение пусто; пустые значения уникальный индекс не ловит — дозаполните их командой onebase renumber",
			e.Name, st.Field, st.Empty)
	}
	if st.Duplicates > 0 {
		return i18nerr.Errorf(
			"%s: уникальность %s включена, но в базе уже есть %d повторяющихся значений; исправьте их до включения",
			e.Name, st.Field, st.Duplicates)
	}
	return nil
}

// UniqueCodeIndexSpec возвращает индекс, который нужно создать по
// numerator.unique. Второй результат — нужен ли он вообще.
func UniqueCodeIndexSpec(e *metadata.Entity) (metadata.IndexSpec, bool) {
	field := uniqueCodeIndexField(e)
	if field == "" {
		return metadata.IndexSpec{}, false
	}
	fields := []string{declaredFieldName(e, field)}
	// Со scope: счётчик свой в каждом разрезе, поэтому «Р-000001» законно
	// существует у каждой организации. Глобальный индекс отклонил бы первый же
	// документ второй организации — данные при этом верны, сломана была бы
	// проверка. Уникальность здесь составная: разрез + значение.
	if e.Numerator.Scope != "" {
		if scope := findFieldFold(e, e.Numerator.Scope); scope != "" {
			fields = append([]string{scope}, fields...)
		}
	}
	// Уже объявленный вручную индекс по тому же набору полей не дублируем: две
	// одинаковые уникальности дали бы два индекса на одну гарантию.
	for _, idx := range e.Indexes {
		if idx.Unique && sameFieldSet(idx.Fields, fields) {
			return metadata.IndexSpec{}, false
		}
	}
	return metadata.IndexSpec{Fields: fields, Unique: true}, true
}

// sameFieldSet сравнивает наборы полей без учёта регистра и порядка.
func sameFieldSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if strings.EqualFold(x, y) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// findFieldFold возвращает объявленное имя реквизита без учёта регистра, пусто —
// если такого нет.
func findFieldFold(e *metadata.Entity, name string) string {
	for _, f := range e.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Name
		}
	}
	return ""
}

// declaredFieldName возвращает написание поля так, как оно объявлено в
// метаданных: построение индекса ищет поле по ТОЧНОМУ совпадению имени, а
// AutoNumberField отдаёт каноническое «Код»/«Номер». Конфигурация, написавшая
// «код» строчными, иначе роняла бы миграцию на "index references unknown field".
func declaredFieldName(e *metadata.Entity, field string) string {
	if name := findFieldFold(e, field); name != "" {
		return name
	}
	return field
}

// columnOfField возвращает имя колонки по имени реквизита без учёта регистра.
func columnOfField(e *metadata.Entity, name string) string {
	for _, f := range e.Fields {
		if strings.EqualFold(f.Name, name) {
			return metadata.ColumnName(f)
		}
	}
	return ""
}

// uniqueViolationField определяет, ПО КАКОМУ полю сработала уникальность.
//
// Оба способа опираются на данные, а не на локализованный текст, и не требуют
// дополнительного запроса: после сбоя внутри транзакции PostgreSQL отклоняет
// любой следующий запрос до отката, так что «сходить и посмотреть, что там уже
// лежит» на этом пути невозможно в принципе.
//
//   - PostgreSQL отдаёт ИМЯ индекса, а имена мы генерируем сами (stableIndexName)
//     — считаем ожидаемое имя для каждого кандидата и сравниваем;
//   - SQLite имени индекса не даёт, зато пишет «таблица.колонка» в тексте, и
//     этот текст английский всегда, без локализации.
func uniqueViolationField(err error, e *metadata.Entity) (field string, value string, ok bool) {
	if e == nil {
		return "", "", false
	}
	table := metadata.TableName(e.Name)

	candidates := append([]metadata.IndexSpec{}, e.Indexes...)
	if spec, need := UniqueCodeIndexSpec(e); need {
		candidates = append(candidates, spec)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName != "" {
		for _, idx := range candidates {
			if !idx.Unique || len(idx.Fields) == 0 {
				continue
			}
			cols, cerr := entityIndexColumns(e, idx)
			if cerr != nil {
				continue
			}
			if stableIndexName(table, cols, true) == pgErr.ConstraintName {
				// Показываем само значение, а не разрез: занят именно код.
				return idx.Fields[len(idx.Fields)-1], "", true
			}
		}
		return "", "", false
	}

	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		const marker = "UNIQUE constraint failed: "
		text := sqErr.Error()
		i := strings.Index(text, marker)
		if i < 0 {
			return "", "", false
		}
		// «таблица.колонка[, таблица.колонка…] (2067)» — отрезаем хвост с
		// числовым кодом, который драйвер дописывает к тексту, и берём
		// ПОСЛЕДНЮЮ колонку: у составного индекса (разрез + код) занят код, а
		// разрез сам по себе неуникален и в сообщении бесполезен.
		spec := strings.TrimSpace(text[i+len(marker):])
		if j := strings.LastIndex(spec, " ("); j >= 0 {
			spec = spec[:j] // хвост « (2067)»; перечисление колонок разделено «, »
		}
		parts := strings.Split(spec, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		col := last
		if j := strings.LastIndex(last, "."); j >= 0 {
			col = last[j+1:]
		}
		for _, f := range e.Fields {
			if strings.EqualFold(metadata.ColumnName(f), strings.TrimSpace(col)) {
				return f.Name, "", true
			}
		}
	}
	return "", "", false
}

// ExplainUniqueViolation переводит отказ БД по уникальности в текст для
// человека. Ошибки не про дубль возвращаются как есть.
func ExplainUniqueViolation(err error, e *metadata.Entity, fields map[string]any) error {
	if err == nil || e == nil || !IsUniqueViolation(err) {
		return err
	}
	field, _, ok := uniqueViolationField(err, e)
	if !ok {
		return err // непонятно, по какому полю — сырую ошибку не подменяем
	}
	value := ""
	for k, v := range fields {
		if strings.EqualFold(k, field) && v != nil {
			value = strings.TrimSpace(fmt.Sprintf("%v", v))
			break
		}
	}
	classified, _ := ClassifyUniqueViolation(err, e.Name, field, value)
	return classified
}

// ClassifyUniqueViolation превращает ошибку драйвера о нарушении уникальности в
// сообщение для человека. Возвращает (nil, false), если ошибка не про дубль.
//
// Пользователь, который ввёл занятый код, не обязан знать ни про индексы, ни
// про SQLSTATE: без этого перевода он видел бы «UNIQUE constraint failed:
// контрагенты.код» и не понимал, что делать.
func ClassifyUniqueViolation(err error, entityName, field, value string) (error, bool) {
	if err == nil || !IsUniqueViolation(err) {
		return nil, false
	}
	if value != "" {
		return duplicateError{i18nerr.Errorf(
			"%s: значение %s «%s» уже занято другой записью", entityName, field, value)}, true
	}
	return duplicateError{i18nerr.Errorf(
		"%s: значение %s уже занято другой записью", entityName, field)}, true
}

// dropStaleUniqueCodeIndexes снимает уникальный индекс, который эта же миграция
// когда-то создала, а теперь не создаёт: `unique: true` убрали из YAML, или
// изменился разрез. CREATE INDEX IF NOT EXISTS сам ничего не удаляет, поэтому
// без этого снятие флага не давало бы НИЧЕГО — записи продолжали бы
// отклоняться, и автор конфигурации искал бы причину в коде, а не в базе. Ровно
// тот класс дефектов («объявил — не работает»), только наоборот (Д10).
//
// Удаляются лишь имена, которые мы могли сгенерировать сами и которых нет среди
// создаваемых сейчас: чужие и объявленные вручную индексы не трогаются.
func (db *DB) dropStaleUniqueCodeIndexes(ctx context.Context, e *metadata.Entity, keep map[string]bool) error {
	if e == nil {
		return nil
	}
	std := metadata.StandardCodeField
	if e.Kind == metadata.KindDocument {
		std = "Номер"
	}
	col := strings.ToLower(std)
	if c := columnOfField(e, std); c != "" {
		col = c
	}
	table := metadata.TableName(e.Name)

	// Разрезы, в которых индекс мог быть создан прежними версиями конфигурации:
	// само значение и «любой объявленный реквизит + значение». Ограничиться
	// текущим scope нельзя: после смены scope прежний составной индекс остался бы
	// в базе и продолжал отклонять записи уже по старым правилам.
	candidates := [][]string{{col}}
	for _, f := range e.Fields {
		c := metadata.ColumnName(f)
		if c == col {
			continue
		}
		candidates = append(candidates, []string{c, col})
	}
	for _, cols := range candidates {
		name := stableIndexName(table, cols, true)
		if keep[name] {
			continue
		}
		if _, err := db.Exec(ctx, "DROP INDEX IF EXISTS "+name); err != nil {
			return err
		}
	}
	return nil
}

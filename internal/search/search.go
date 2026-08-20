// Package search — глобальный (полнотекстовый) поиск по данным базы, план 82.
//
// Индекс лежит в storage и о правах ничего не знает: он одинаков для всех
// пользователей. Права накладываются здесь, в один слой на все точки входа —
// UI, REST и DSL, — чтобы выдача не разъезжалась между ними. Уровней три:
//
//  1. объектный RBAC — сущность без права read вообще не участвует в запросе;
//  2. строковые политики (план 79) — каждая найденная строка перечитывается и
//     проверяется политикой, как в списках;
//  3. маскирование реквизитов (план 88) — совпадение по скрытому от
//     пользователя реквизиту не должно превращать поиск в оракул: такие
//     совпадения отбрасываются.
package search

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Result — одно совпадение, уже разрешённое к показу пользователю.
type Result struct {
	Kind         string // catalog | document
	Entity       string // имя сущности
	ID           uuid.UUID
	Title        string // представление объекта (с учётом маскирования)
	DeletionMark bool
	Posted       bool
	IsDocument   bool
}

// Page — страница выдачи.
type Page struct {
	Items []Result
	// NextOffset — смещение в индексе для следующей страницы. Считается по
	// просмотренным строкам индекса, а не по показанным: часть строк отсеяли
	// права, и продолжать надо с того места, где чтение остановилось.
	//
	// НАРУЖУ ЭТО ЧИСЛО НЕ ОТДАЁТСЯ: разница «просмотрено» и «показано» — оракул
	// по скрытым значениям (см. cursor.go). Точки входа обязаны публиковать
	// только Cursor.
	NextOffset int
	// Cursor — непрозрачная позиция чтения для следующей страницы. Пусто, если
	// продолжения нет.
	Cursor  string
	HasMore bool
}

// Deps — то, чего поиск не умеет сам: права и метаданные. Реализуется
// ui.Server и api.handler поверх их собственных проверок, поэтому глобальный
// поиск отдаёт ровно то же, что отдали бы списки этих объектов.
type Deps interface {
	// Entities возвращает все сущности конфигурации.
	Entities() []*metadata.Entity
	// CanRead — объектный RBAC: доступна ли сущность пользователю на чтение.
	CanRead(ctx context.Context, e *metadata.Entity) bool
	// RowAllowed — строковая политика (план 79) для конкретной строки.
	RowAllowed(ctx context.Context, e *metadata.Entity, row map[string]any) bool
	// MaskedIndexedFields возвращает индексируемые реквизиты, скрытые или
	// замаскированные для пользователя. Пусто — маскирования нет.
	MaskedIndexedFields(ctx context.Context, e *metadata.Entity) []string
	// MaskedLabel маскирует строку на месте и возвращает представление объекта.
	MaskedLabel(ctx context.Context, e *metadata.Entity, row map[string]any) string
}

// scanBudgetFactor задаёт бюджет просмотра индекса на один запрос — во сколько
// раз больше страницы поиск готов прочитать, добирая её сквозь строки, скрытые
// правами. Бюджет считается в ПРОСМОТРЕННЫХ строках индекса (≈ прежние пять
// пачек по 2×limit): если пользователю почти ничего не видно, поиск не
// вычитывает весь индекс. Он же — граница, за которой has_more больше ничего не
// сообщает о скрытых правами строках (issue #578, см. ниже).
const scanBudgetFactor = 10

// EqualFilter сужает полнотекстовый индекс равенством по реквизиту исходной
// записи. Entities nil означает все доступные сущности с таким реквизитом;
// непустой список дополнительно ограничивает типы объектов. Сущности без поля
// исключаются — они не должны незаметно попасть в выдачу без отбора.
type EqualFilter struct {
	Field    string
	Value    any
	Entities []string
}

// Run выполняет глобальный поиск с учётом прав пользователя из ctx.
// Продолжение листания задаётся курсором из предыдущего ответа, а не числом:
// разбирает его сам Run, своими же text и limit. Это не удобство — иначе точка
// входа могла бы расшифровать курсор одним запросом, а искать другим, и
// привязка курсора к запросу (cursorScope) ничего бы не значила.
func Run(ctx context.Context, store *storage.DB, deps Deps, text string, limit int, cursor string) (Page, error) {
	return run(ctx, store, deps, text, limit, cursor, nil)
}

// RunFiltered выполняет поиск с равенством по реквизиту исходного объекта.
// Отбор компилируется storage в EXISTS внутри FTS-запроса до ORDER BY/LIMIT;
// постфильтрация здесь снова допустила бы starvation от чужих tenant-ов.
// Листание намеренно не принимается: публичный cursorScope пока не кодирует
// параметры отбора, а неполная привязка курсора была бы небезопасной.
func RunFiltered(ctx context.Context, store *storage.DB, deps Deps, text string, limit int, filter EqualFilter) (Page, error) {
	return run(ctx, store, deps, text, limit, "", &filter)
}

func run(ctx context.Context, store *storage.DB, deps Deps, text string, limit int, cursor string, filter *EqualFilter) (Page, error) {
	if store == nil || deps == nil || strings.TrimSpace(text) == "" {
		return Page{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	// Область привязки считается ПОСЛЕ нормализации limit: иначе limit=0 и
	// limit=20 дали бы разные области при одинаковом поведении, и листание
	// обрывалось бы на втором шаге.
	scope := cursorScope{Text: text, Limit: limit}
	offset := decodeCursor(cursor, scope)

	byName := make(map[string]*metadata.Entity)
	var names []string
	var scopes []storage.FTSScope
	allowedNames := make(map[string]bool)
	if filter != nil && filter.Entities != nil {
		for _, name := range filter.Entities {
			name = strings.TrimSpace(name)
			if name != "" {
				allowedNames[strings.ToLower(name)] = true
			}
		}
	}
	for _, e := range deps.Entities() {
		if e == nil || !deps.CanRead(ctx, e) {
			continue
		}
		if filter != nil && filter.Entities != nil && !allowedNames[strings.ToLower(e.Name)] {
			continue
		}
		if len(metadata.FullTextFields(e)) == 0 {
			continue
		}
		if filter != nil {
			field, ok := entityFilterField(e, filter.Field)
			if !ok {
				continue
			}
			scopes = append(scopes, storage.FTSScope{
				Entity: e,
				Predicate: storage.Predicate{
					Field: field.Name,
					Op:    "eq",
					Value: filter.Value,
				},
			})
		}
		names = append(names, e.Name)
		byName[e.Name] = e
	}
	if len(names) == 0 {
		return Page{}, nil
	}

	page := Page{NextOffset: offset}
	// Строки, отсеянные правами, не должны укорачивать страницу — добираем
	// следующие строки индекса, пока не наберём limit либо не исчерпаем бюджет
	// просмотра (scanBudgetFactor) или сам индекс.
	batch := limit * 2
	maxScan := limit * scanBudgetFactor
	scanned := 0
	for scanned < maxScan && len(page.Items) < limit {
		n := batch
		if rem := maxScan - scanned; n > rem {
			n = rem
		}
		hits, err := store.SearchFullText(ctx, storage.FTSQuery{
			Text:   text,
			Names:  names,
			Scopes: scopes,
			Limit:  n,
			Offset: page.NextOffset,
		})
		if err != nil {
			return Page{}, err
		}
		if len(hits) == 0 {
			break // индекс дочитан
		}
		for _, hit := range hits {
			page.NextOffset++
			scanned++
			e := byName[hit.Name]
			if e == nil {
				continue
			}
			res, ok := resolveHit(ctx, store, deps, e, hit, text)
			if !ok {
				continue
			}
			page.Items = append(page.Items, res)
			if len(page.Items) == limit {
				break // страница набрана; NextOffset — где остановились
			}
		}
		if len(hits) < n {
			break // индекс дочитан внутри пачки
		}
	}
	// has_more — набралась ли ПОЛНАЯ видимая страница в пределах бюджета.
	// Сознательно НЕ зависит от того, сколько строк отсеяли права: иначе пустая
	// выдача с «есть ещё» отличала бы скрытое правами совпадение от заведомо
	// отсутствующего — оракул по маске (план 88) или строковой политике
	// (план 79), причём при любом числе совпадений (issue #578). Цена размена:
	// пользователь с очень узкой политикой может не увидеть «есть ещё», когда
	// его видимые совпадения лежат дальше бюджета просмотра; добор ограничен
	// бюджетом, а точную позицию всё равно прячет курсор.
	page.HasMore = len(page.Items) == limit
	return withCursor(page, scope), nil
}

func entityFilterField(e *metadata.Entity, name string) (metadata.Field, bool) {
	name = strings.TrimSpace(name)
	if e == nil || name == "" {
		return metadata.Field{}, false
	}
	for _, field := range e.Fields {
		if strings.EqualFold(field.Name, name) {
			return field, true
		}
	}
	return metadata.Field{}, false
}

// withCursor проставляет непрозрачную позицию чтения. Вызывается на каждом
// выходе из Run, чтобы наружу не уехало сырое смещение.
func withCursor(page Page, sc cursorScope) Page {
	if page.HasMore {
		page.Cursor = encodeCursor(page.NextOffset, sc)
	}
	return page
}

// resolveHit перечитывает строку и проверяет права. Именно перечитывание, а
// не доверие индексу, даёт свежие данные (представление, пометка удаления) и
// строку для строковых политик.
func resolveHit(ctx context.Context, store *storage.DB, deps Deps, e *metadata.Entity, hit storage.FTSHit, text string) (Result, bool) {
	row, err := store.GetByID(ctx, e.Name, hit.ID, e)
	if err != nil || row == nil {
		// Объекта уже нет (удалён мимо платформы) — показывать нечего.
		return Result{}, false
	}
	if !deps.RowAllowed(ctx, e, row) {
		return Result{}, false
	}
	masked := deps.MaskedIndexedFields(ctx, e)
	if len(masked) > 0 && !visibleMatch(text, e, row, masked) {
		return Result{}, false
	}
	res := Result{
		Kind:         string(e.Kind),
		Entity:       e.Name,
		ID:           hit.ID,
		DeletionMark: asBool(row["deletion_mark"]),
		IsDocument:   e.Kind == metadata.KindDocument,
	}
	if res.IsDocument {
		res.Posted = asBool(row["posted"])
	}
	// MaskedLabel маскирует row до того, как значение попадёт в представление.
	res.Title = deps.MaskedLabel(ctx, e, row)
	if strings.TrimSpace(res.Title) == "" {
		res.Title = hit.ID.String()
	}
	return res, true
}

// visibleMatch проверяет, что запрос находится в видимой пользователю части
// текста объекта. Нужно, когда часть индексируемых реквизитов замаскирована:
// иначе поиск отвечал бы «есть такой объект» на скрытое значение и работал бы
// как оракул для подбора (телефон, ИНН, паспорт).
//
// Проверка грубее самого индекса — сравнение по префиксу слова, без
// морфологии, — поэтому применяется только при наличии маскирования: цена
// ошибки здесь ложный отрицательный ответ, а не утечка.
func visibleMatch(text string, e *metadata.Entity, row map[string]any, masked []string) bool {
	hidden := make(map[string]bool, len(masked))
	for _, name := range masked {
		hidden[strings.ToLower(name)] = true
	}
	var visible strings.Builder
	for _, f := range metadata.FullTextFields(e) {
		if hidden[strings.ToLower(f.Name)] {
			continue
		}
		v, ok := row[f.Name]
		if !ok || v == nil {
			continue
		}
		visible.WriteString(strings.ToLower(toText(v)))
		visible.WriteByte(' ')
	}
	words := tokens(visible.String())
	for _, want := range tokens(text) {
		found := false
		for _, w := range words {
			if strings.HasPrefix(w, want) {
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

func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func toText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	}
	return fmt.Sprintf("%v", v)
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	case string:
		return t == "true" || t == "1"
	}
	return false
}

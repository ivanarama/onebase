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
	NextOffset int
	HasMore    bool
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

// maxBatches ограничивает добор строк при отсеве правами: если пользователю
// не видно почти ничего, поиск не должен вычитывать весь индекс.
const maxBatches = 5

// Run выполняет глобальный поиск с учётом прав пользователя из ctx.
func Run(ctx context.Context, store *storage.DB, deps Deps, text string, limit, offset int) (Page, error) {
	if store == nil || deps == nil || strings.TrimSpace(text) == "" {
		return Page{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	byName := make(map[string]*metadata.Entity)
	var names []string
	for _, e := range deps.Entities() {
		if e == nil || !deps.CanRead(ctx, e) {
			continue
		}
		if len(metadata.FullTextFields(e)) == 0 {
			continue
		}
		names = append(names, e.Name)
		byName[e.Name] = e
	}
	if len(names) == 0 {
		return Page{}, nil
	}

	page := Page{NextOffset: offset}
	// Строки, отсеянные правами, не должны укорачивать страницу — добираем
	// следующую пачку, пока не наберём limit или не кончится индекс.
	batch := limit * 2
	for i := 0; i < maxBatches && len(page.Items) < limit; i++ {
		hits, err := store.SearchFullText(ctx, storage.FTSQuery{
			Text:   text,
			Names:  names,
			Limit:  batch,
			Offset: page.NextOffset,
		})
		if err != nil {
			return Page{}, err
		}
		if len(hits) == 0 {
			return page, nil
		}
		for _, hit := range hits {
			page.NextOffset++
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
				// Индекс мог не кончиться — следующая страница начнётся с
				// NextOffset, поэтому уже просмотренное не повторится.
				page.HasMore = true
				return page, nil
			}
		}
		if len(hits) < batch {
			return page, nil
		}
	}
	// Пачки кончились раньше страницы: правами отсеяло почти всё, но индекс
	// прочитан не до конца. Признак «есть ещё» здесь обязателен — иначе
	// пользователю с узкой политикой выдача врала бы «больше ничего нет».
	page.HasMore = true
	return page, nil
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

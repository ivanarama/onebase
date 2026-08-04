package access

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

// QueryMaskPlan — решение полевого маскирования для результата запроса
// (план 88E). Строится ДО выполнения SQL: Denied отклоняет запрос целиком,
// иначе Apply маскирует колонки уже прочитанных строк.
//
// Почему маска накладывается в Go, а не выражением в SELECT: стратегия
// mask_city разворачивается прикладным хуком нормализации адреса
// (SetAddressCityFunc) — SQL-эквивалента у неё нет. Маскирование в одном месте
// с остальными путями чтения (MaskRecord) заодно гарантирует, что отчёт, форма
// и REST показывают одно и то же значение на SQLite и на PostgreSQL.
type QueryMaskPlan struct {
	// Denied — логическое поле, из-за которого запрос выполнять нельзя: оно
	// защищено маской, но участвует в отборе/группировке/агрегате либо запрос
	// слишком сложен для поколоночного разбора. Пусто — запрос разрешён.
	Denied string
	// byColumn — решения по имени результирующей колонки (нижний регистр).
	byColumn map[string]FieldDecision
	// byField — решения по именам полей для проекции «*»: имена колонок
	// результата там совпадают с колонками таблиц.
	byField map[string]FieldDecision
}

// Empty сообщает, что маскировать нечего и запрос разрешён.
func (p QueryMaskPlan) Empty() bool {
	return p.Denied == "" && len(p.byColumn) == 0 && len(p.byField) == 0
}

// QueryMaskPlanFor строит план маскирования результата скомпилированного
// запроса для пользователя u. lookup разрешает метаданные объекта-источника.
func QueryMaskPlanFor(u *auth.User, res query.Result, lookup func(kind, name string) *metadata.Entity) QueryMaskPlan {
	masked, columns := maskedSourceFields(u, res.Sources, lookup)
	if len(masked) == 0 {
		return QueryMaskPlan{}
	}
	if !res.Projection.Simple {
		// Подзапрос/ОБЪЕДИНИТЬ: сопоставить элемент выборки с колонкой
		// результата нельзя — остаётся прежний fail-closed отказ (план 88D).
		// Но одного разбора списка выборки мало: DeniedMaskedColumn не смотрит
		// ГДЕ, поэтому фиктивный подзапрос снимал отказ по отбору. Сначала —
		// защищённое поле в любом месте такого запроса.
		for _, field := range res.Projection.UnmaskableFields {
			if _, ok := masked[fieldKey(field)]; ok {
				return QueryMaskPlan{Denied: field}
			}
		}
		return QueryMaskPlan{Denied: DeniedMaskedColumn(u, res.Sources, res.ProjectionFields, lookup)}
	}
	// Поле под маской в отборе/группировке/сортировке/агрегате: маска на выходе
	// его не защищает — значение либо перебирается условием, либо уже свёрнуто.
	// Хвост запроса ссылается на колонку и по выходному алиасу, и по номеру —
	// оба варианта дают тот же оракул, что и ссылка по имени поля.
	aliasFields := map[string][]string{}
	for _, col := range res.Projection.Columns {
		if col.Output != "" && len(col.Fields) > 0 {
			aliasFields[fieldKey(col.Output)] = col.Fields
		}
	}
	for _, name := range res.Projection.UnmaskableFields {
		if _, ok := masked[fieldKey(name)]; ok {
			return QueryMaskPlan{Denied: name}
		}
		if fields, ok := aliasFields[fieldKey(name)]; ok {
			if denied, hit := firstMaskedField(masked, fields); hit {
				return QueryMaskPlan{Denied: denied}
			}
		}
	}
	for _, n := range res.Projection.UnmaskableOrdinals {
		if n < 1 || n > len(res.Projection.Columns) {
			continue
		}
		col := res.Projection.Columns[n-1]
		if col.Star {
			// «ВЫБРАТЬ * … УПОРЯДОЧИТЬ ПО 1»: какая колонка таблицы окажется
			// первой, разбор не знает — путь fail-closed.
			return QueryMaskPlan{Denied: anyMaskedName(masked)}
		}
		if denied, hit := firstMaskedField(masked, col.Fields); hit {
			return QueryMaskPlan{Denied: denied}
		}
	}
	plan := QueryMaskPlan{}
	for _, col := range res.Projection.Columns {
		if col.Star {
			plan.byField = columns
			continue
		}
		dec, ok := mostRestrictive(masked, col.Fields)
		if !ok {
			continue
		}
		if col.Output == "" {
			// Защищённое поле в выражении без алиаса — колонку не адресовать.
			return QueryMaskPlan{Denied: col.Fields[0]}
		}
		if plan.byColumn == nil {
			plan.byColumn = map[string]FieldDecision{}
		}
		if prev, exists := plan.byColumn[col.Output]; !exists || lessRestrictive(prev, dec) {
			plan.byColumn[col.Output] = dec
		}
	}
	return plan
}

// Apply маскирует строки результата на месте. Возвращает ошибку, если
// запланированная колонка отсутствует в результате: расхождение разбора и
// фактических колонок означает, что защищённое значение могло уйти под другим
// именем, поэтому путь fail-closed — вызывающий обязан не отдавать строки.
//
// Стратегия hide обнуляет значение, а не удаляет ключ: форма таблицы отчёта
// (колонки, группировки, выгрузка) не должна меняться от роли читателя.
func (p QueryMaskPlan) Apply(rows []map[string]any) error {
	if p.Denied != "" {
		return fmt.Errorf("запрос отклонён: защищённое поле %q", p.Denied)
	}
	if len(rows) == 0 || p.Empty() {
		return nil
	}
	for column, dec := range p.byColumn {
		key, ok := matchRowKey(rows[0], column)
		if !ok {
			return fmt.Errorf("защищённая колонка %q не найдена в результате запроса", column)
		}
		for _, row := range rows {
			applyDecision(row, key, dec)
		}
	}
	for field, dec := range p.byField {
		for _, row := range rows {
			if key, ok := matchRowKey(row, field); ok {
				applyDecision(row, key, dec)
			}
		}
	}
	return nil
}

func applyDecision(row map[string]any, key string, dec FieldDecision) {
	if dec.Hidden() {
		row[key] = nil
		return
	}
	row[key] = MaskValue(dec.Strategy, dec.Keep, row[key])
}

// maskedSourceFields собирает решения по всем источникам запроса: по логическому
// имени поля и, отдельно, по имени колонки таблицы (для проекции «*»).
func maskedSourceFields(u *auth.User, sources []query.SourceRef, lookup func(kind, name string) *metadata.Entity) (byField, byColumn map[string]FieldDecision) {
	if u == nil || (u.IsAdmin && !MaskAdmin()) {
		return nil, nil
	}
	byField = map[string]FieldDecision{}
	byColumn = map[string]FieldDecision{}
	put := func(m map[string]FieldDecision, key string, dec FieldDecision) {
		key = fieldKey(key)
		if prev, ok := m[key]; ok && !lessRestrictive(prev, dec) {
			return
		}
		m[key] = dec
	}
	for _, src := range sources {
		var meta *metadata.Entity
		if lookup != nil {
			meta = lookup(src.Kind, src.Name)
		}
		for field, dec := range FieldDecisions(u, src.Kind, src.Name, meta) {
			put(byField, field, dec)
			if f, ok := concreteMetaField(meta, field); ok {
				put(byColumn, metadata.ColumnName(f), dec)
			} else {
				put(byColumn, field, dec)
			}
		}
	}
	if len(byField) == 0 {
		return nil, nil
	}
	return byField, byColumn
}

// firstMaskedField возвращает первое из полей, на которое стоит маска.
func firstMaskedField(masked map[string]FieldDecision, fields []string) (string, bool) {
	for _, field := range fields {
		if _, ok := masked[fieldKey(field)]; ok {
			return field, true
		}
	}
	return "", false
}

// anyMaskedName — имя защищённого поля для сообщения об отказе, когда конкретную
// колонку назвать нельзя. Выбирается детерминированно, чтобы текст отказа не
// плясал от обхода карты.
func anyMaskedName(masked map[string]FieldDecision) string {
	best := ""
	for key := range masked {
		if best == "" || key < best {
			best = key
		}
	}
	return best
}

// mostRestrictive выбирает самое строгое решение среди полей, которые питают
// одну колонку результата (например само ссылочное поле и Наименование
// связанной сущности).
func mostRestrictive(masked map[string]FieldDecision, fields []string) (FieldDecision, bool) {
	var best FieldDecision
	found := false
	for _, field := range fields {
		dec, ok := masked[fieldKey(field)]
		if !ok {
			continue
		}
		if !found || lessRestrictive(best, dec) {
			best, found = dec, true
		}
	}
	return best, found
}

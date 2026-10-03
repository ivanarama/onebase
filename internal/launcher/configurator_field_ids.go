package launcher

// Устойчивые идентификаторы реквизитов в конфигураторе (план 81).
//
// Редактор реквизитов пересобирает список полей из формы, а не правит YAML
// точечно. Значит, всё, чего форма не знает, при сохранении теряется — и `id`
// потерялся бы первым, разорвав связь поля с колонкой ровно в тот момент, когда
// она нужна. Поэтому идентификатор переносится из прежнего состояния файла по
// имени реквизита (переименование в этом редакторе недоступно: имя приходит
// скрытым полем и не меняется), а новым реквизитам выдаётся свой.
//
// Побочный полезный эффект: любое сохранение объекта в конфигураторе
// проставляет id всем его реквизитам. Для существующей базы это безопасно —
// миграция увидит «id неизвестен, колонка на месте» и просто запомнит
// соответствие, ничего не меняя. То есть достаточно один раз открыть и
// сохранить объект, чтобы его будущие переименования сохраняли данные.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

func splitStandardSaveFields(fields []saveField, name string) ([]saveField, *saveField, error) {
	ordinary := make([]saveField, 0, len(fields))
	var standard *saveField
	for i := range fields {
		f := fields[i]
		if strings.EqualFold(strings.TrimSpace(f.Name), name) {
			if standard != nil {
				return nil, nil, fmt.Errorf("multiple %s fields alongside numerator", name)
			}
			standard = &fields[i]
		} else {
			ordinary = append(ordinary, f)
		}
	}
	return ordinary, standard, nil
}

func standardSaveField(name, id string, properties *saveStandardField) saveField {
	f := saveField{Name: name, ID: id, Type: "string"}
	if properties != nil {
		f.Title, f.Label, f.Titles = properties.Title, properties.Label, properties.Titles
		f.Required, f.Default, f.PII = properties.Required, properties.Default, properties.PII
	}
	return f
}

func standardProperties(f saveField) *saveStandardField {
	if f.Title == "" && f.Label == "" && len(f.Titles) == 0 && !f.Required && f.Default == "" && !f.PII {
		return nil
	}
	return &saveStandardField{
		Title: f.Title, Label: f.Label, Titles: f.Titles,
		Required: f.Required, Default: f.Default, PII: f.PII,
	}
}

// applyStandardFieldEdits keeps the standard column's identity while moving
// its user properties between numerator.field and fields. The same function
// runs for file and database configuration storage.
func applyStandardFieldEdits(ent *saveEntity, kind metadata.Kind, fields []saveField, numerator **saveNumerator) error {
	hadNumerator := ent.Numerator != nil
	willNumerator := hadNumerator
	if numerator != nil {
		willNumerator = *numerator != nil
	}
	name, id := standardFieldIdentity(kind, hadNumerator || willNumerator)
	if name == "" {
		ent.Fields = ensureFieldIDs(ent.Fields, fields)
		if numerator != nil {
			ent.Numerator = *numerator
		}
		return nil
	}
	previousOrdinary, previousStandard, err := splitStandardSaveFields(ent.Fields, name)
	if err != nil {
		return err
	}
	nextOrdinary, nextStandard, err := splitStandardSaveFields(fields, name)
	if err != nil {
		return err
	}
	if hadNumerator && ent.Numerator.Field != nil && previousStandard != nil {
		return fmt.Errorf("%s is defined in both fields and numerator.field", name)
	}
	standard := standardSaveField(name, id, nil)
	if previousStandard != nil {
		standard = *previousStandard
	} else if hadNumerator {
		standard = standardSaveField(name, id, ent.Numerator.Field)
	}
	if nextStandard != nil {
		if nextStandard.Type != "" && nextStandard.Type != "string" {
			return fmt.Errorf("standard field %s must have type string", name)
		}
		previousTitles := standard.Titles
		standard = carryFieldKeys(*nextStandard, standard)
		// During first numbering the ordinary row also omits hidden languages.
		standard.Titles = mergeStandardTitles(previousTitles, standard.Titles)
	}
	standard.Name, standard.ID, standard.Type = name, id, "string"
	if willNumerator {
		active := ent.Numerator
		if numerator != nil {
			active = *numerator
		}
		if active.Field != nil && numerator != nil {
			// Keep the legacy label spelling on an unchanged roundtrip. A
			// changed form title, including an explicit clear, supersedes it.
			previousTitle := standard.Title
			if previousTitle == "" {
				previousTitle = standard.Label
			}
			if active.Field.Title != previousTitle {
				standard.Title, standard.Label = active.Field.Title, ""
			}
			if active.Field.TitlesPresent {
				standard.Titles = mergeStandardTitles(standard.Titles, active.Field.Titles)
			}
		}
		active.Field = standardProperties(standard)
		ent.Numerator = active
		ent.Fields = ensureFieldIDs(previousOrdinary, nextOrdinary)
		return nil
	}
	// Turning numbering off retains the same physical column and stable ID.
	ent.Numerator = nil
	ent.Fields = append([]saveField{standard}, ensureFieldIDs(previousOrdinary, nextOrdinary)...)
	return nil
}

// Only submitted languages are edits. Empty submitted values clear a visible
// translation; absent languages (including ru) keep their stored values.
func mergeStandardTitles(previous, edits map[string]string) map[string]string {
	titles := maps.Clone(previous)
	if titles == nil {
		titles = make(map[string]string)
	}
	for lang, value := range edits {
		if value == "" {
			delete(titles, lang)
		} else {
			titles[lang] = value
		}
	}
	if len(titles) == 0 {
		return nil
	}
	return titles
}

// ensureFieldIDs возвращает next с проставленными id: перенесёнными из prev по
// имени реквизита либо сгенерированными. Заодно переносит ключи, которых
// редактор не знает и потому не прислал бы обратно, — их список в
// carryFieldKeys.
func ensureFieldIDs(prev, next []saveField) []saveField {
	byName := make(map[string]saveField, len(prev))
	used := make(map[string]bool, len(prev))
	for _, f := range prev {
		key := strings.ToLower(strings.TrimSpace(f.Name))
		byName[key] = carryFieldKeys(f, byName[key])
		if f.ID != "" {
			used[f.ID] = true
		}
	}
	out := make([]saveField, len(next))
	copy(out, next)
	for i := range out {
		key := strings.ToLower(strings.TrimSpace(out[i].Name))
		out[i] = carryFieldKeys(out[i], byName[key])
		if out[i].ID == "" {
			out[i].ID = newFieldID(used)
		}
		used[out[i].ID] = true
	}
	return out
}

// carryFieldKeys дополняет f незаполненными ключами из прежнего состояния
// файла old. Значение из формы главнее: иначе редактор, когда научится править
// эти ключи, не смог бы их изменить.
//
// Перенос односторонний, и это осознанная граница. Снять через конфигуратор
// `required`, `pii` или подпись нельзя — он их не показывает, а значит
// «редактор не прислал ключ» и «пользователь снял галочку» приходят одним и тем
// же пустым значением. Регрессом односторонность не является: до переноса эти
// ключи нельзя было ни снять осознанно, ни сохранить — они пропадали при любом
// сохранении объекта. Полноценное редактирование (чекбокс обязательности, поле
// подписи) — отдельная возможность, а не эта починка.
func carryFieldKeys(f, old saveField) saveField {
	if f.ID == "" {
		f.ID = old.ID
	}
	if f.Title == "" {
		f.Title = old.Title
	}
	if f.Label == "" {
		f.Label = old.Label
	}
	if f.Default == "" {
		f.Default = old.Default
	}
	if !f.Required {
		f.Required = old.Required
	}
	if !f.PII {
		f.PII = old.PII
	}
	return f
}

// standardFieldIdentity returns the platform-owned field for a numbered
// entity. Both the previous and submitted numerator state matter: turning
// numbering off must materialize the field with its existing std_* ID.
func standardFieldIdentity(kind metadata.Kind, hasNumerator bool) (name, id string) {
	if !hasNumerator {
		return "", ""
	}
	switch kind {
	case metadata.KindCatalog:
		return metadata.StandardCodeField, metadata.StandardCodeFieldID
	case metadata.KindDocument:
		return metadata.StandardNumberField, metadata.StandardNumberFieldID
	}
	return "", ""
}

// entityKindFromPath определяет вид объекта по каталогу, в котором лежит его
// YAML. Вид приходит и с формы (entity_kind), но брать его оттуда для засева
// нельзя: значение из запроса решало бы, какому реквизиту достанется служебный
// id, то есть за какой колонкой он закрепится. Путь берётся из перечня файлов
// конфигурации и такого выбора пользователю не оставляет.
func entityKindFromPath(p string) metadata.Kind {
	switch strings.ToLower(filepath.Base(filepath.Dir(filepath.ToSlash(p)))) {
	case "catalogs":
		return metadata.KindCatalog
	case "documents":
		return metadata.KindDocument
	}
	return ""
}

// newFieldID выдаёт идентификатор, которого ещё нет в объекте.
//
// Случайный, а не производный от имени: имя русское, а идентификатор обязан
// быть латинским (он попадает в служебную таблицу и в вывод плана миграции), и
// транслитерация дала бы совпадения на похожих названиях — как раз там, где
// уникальность важнее всего.
func newFieldID(used map[string]bool) string {
	for {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			// Источник случайности недоступен — пусть поле останется без id:
			// это вернёт прежнее аддитивное поведение, а не сломает сохранение.
			return ""
		}
		id := "f_" + hex.EncodeToString(b)
		if !used[id] {
			used[id] = true
			return id
		}
	}
}

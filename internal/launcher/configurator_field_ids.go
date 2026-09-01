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
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ensureFieldIDs возвращает next с проставленными id: перенесёнными из prev по
// имени реквизита либо сгенерированными. Заодно переносит ключи, которых
// редактор не знает и потому не прислал бы обратно, — сейчас это `default`
// (план 153) и `pii` (признак ПДн, Field.PII).
func ensureFieldIDs(prev, next []saveField) []saveField {
	byName := make(map[string]string, len(prev))
	defaults := make(map[string]string, len(prev))
	pii := make(map[string]bool, len(prev))
	used := make(map[string]bool, len(prev))
	for _, f := range prev {
		key := strings.ToLower(strings.TrimSpace(f.Name))
		if f.Default != "" {
			defaults[key] = f.Default
		}
		// Перенос односторонний: pii из файла сохраняется, но снять признак
		// через этот редактор нельзя — он его и не показывает. Двусторонний
		// перенос потребовал бы отличать «редактор не прислал ключ» от
		// «пользователь снял галочку», а сейчас это одно и то же значение.
		if f.PII {
			pii[key] = true
		}
		if f.ID == "" {
			continue
		}
		byName[key] = f.ID
		used[f.ID] = true
	}
	out := make([]saveField, len(next))
	copy(out, next)
	for i := range out {
		key := strings.ToLower(strings.TrimSpace(out[i].Name))
		if out[i].Default == "" {
			out[i].Default = defaults[key]
		}
		if !out[i].PII {
			out[i].PII = pii[key]
		}
		if out[i].ID != "" {
			used[out[i].ID] = true
			continue
		}
		if id, ok := byName[key]; ok {
			out[i].ID = id
			continue
		}
		out[i].ID = newFieldID(used)
	}
	return out
}

// withStandardFieldSeed дополняет прежнее состояние файла записью о стандартном
// поле — «Код» справочника, «Номер» документа (#1161).
//
// Такого поля в YAML нет: платформа синтезирует его при загрузке объекта с
// блоком numerator (metadata/yaml.go) и держит за ним устойчивый std_code /
// std_number. Редактор реквизитов рисует загруженную метаданную, поэтому
// синтезированная строка приходит обратно наравне с обычными, а совпадения по
// имени в файле для неё нет — и ensureFieldIDs выдавал ей свежий f_xxxx. При
// следующем старте колонка числилась за std_code, а занимало её поле с чужим
// id: сторож коллизии в schemaplan останавливал миграцию.
//
// Запись идёт ПЕРЕД реальными: ensureFieldIDs заполняет карту имён по порядку,
// поэтому одноимённый реквизит из файла перекроет засев, а не наоборот.
func withStandardFieldSeed(prev []saveField, name, id string) []saveField {
	if name == "" || id == "" {
		return prev
	}
	return append([]saveField{{ID: id, Name: name}}, prev...)
}

// standardFieldSeed возвращает имя и устойчивый id стандартного поля объекта.
// Пустые строки — засев не нужен.
//
// Засев привязан и к виду объекта, и к наличию нумерации: у документа
// стандартное поле зовётся «Номер», поэтому пользовательский реквизит «Код»
// обязан получить собственный id, иначе фикс сам привязал бы его к чужой
// колонке.
//
// hasNumerator означает «блок numerator есть сейчас ИЛИ появится этим
// сохранением»: при снятии нумерации поле остаётся в fields обычным реквизитом,
// но колонка в базе по-прежнему числится за служебным id — свежий f_xxxx дал бы
// ту же коллизию, от которой чинимся.
func standardFieldSeed(kind metadata.Kind, hasNumerator bool) (name, id string) {
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

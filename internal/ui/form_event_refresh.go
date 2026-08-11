package ui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Возврат в форму полей, которые обработчик записал НЕ через себя.
//
// Типичный обработчик команды не правит Объект.Поле, а зовёт общий модуль:
// модуль читает документ из базы, меняет его и записывает. Объект формы при
// этом остаётся с тем, что пришло из POST, а нередактируемое поле-результат
// (disabled → в POST не уходит) ещё до обработчика восстанавливалось из базы —
// то есть значением ДО текущего действия. В ответ уезжало устаревшее значение,
// и поле «отставало на шаг»: после второго нажатия показывало результат первого.
//
// Поэтому после обработчика перечитываем из базы поля, которые (1) не пришли из
// формы и (2) не были присвоены самим обработчиком. Присвоенное обработчиком
// (Объект.Поле = …) трогать нельзя: оно может быть ещё не записано и обязано
// доехать до формы как есть.
//
// Область действия ЭТОЙ функции — РЕКВИЗИТЫ ШАПКИ. Строки табличных частей
// перечитывает refreshTablePartsWrittenByHandler ниже (issue #579): там сложнее,
// потому что надо отличить строки, переписанные модулем в базе, от строк,
// отредактированных пользователем в гриде и ещё не записанных.
//
// Читаем базу второй раз (первый — restoreUnsubmittedFields до обработчика), и
// переиспользовать ту строку нельзя принципиально: смысл в том, чтобы увидеть
// состояние ПОСЛЕ обработчика, а прочитанное до него — ровно то устаревшее
// значение, из-за которого поле и отставало на шаг.

// snapshotFieldValues делает поверхностный снимок значений для сравнения «до/после».
// Значения приводятся к строке: поля сущности скалярны либо ссылки, а сравнение
// интерфейсов напрямую паникует на несравнимых типах.
func snapshotFieldValues(fields map[string]any) map[string]string {
	if fields == nil {
		return nil
	}
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

// readOnlyFormFields собирает имена полей ШАПКИ, отрисованных формой как
// нередактируемые (по последнему сегменту data_path, как checkboxOmittedFields).
//
// Глубина пути проверяется тем же правилом, что и в checkboxOmittedFields:
// поле шапки — это «Объект.Поле». Путь табличной части («Объект.Товары.Цена»)
// к полю шапки отношения не имеет, даже если имя последнего сегмента совпало.
// Без этой проверки readonly-колонка ТЧ делала одноимённое поле шапки «всегда
// перечитываемым», и введённое пользователем значение молча откатывалось к базе.
func readOnlyFormFields(form *metadata.FormModule) map[string]bool {
	out := map[string]bool{}
	if form == nil {
		return out
	}
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		if visit.effectiveReadOnly && el.DataPath != "" && strings.Count(el.DataPath, ".") <= 1 {
			out[strings.ToLower(dpFieldName(el.DataPath))] = true
		}
	})
	return out
}

func (s *Server) refreshFieldsWrittenByHandler(
	ctx context.Context,
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	obj *runtime.Object,
	before map[string]string,
) {
	if s == nil || s.store == nil || entity == nil || obj == nil || obj.Fields == nil {
		return
	}
	if obj.ID == uuid.Nil {
		return
	}
	submitted := submittedFormKeys(r)
	// Ключи поля приходится смотреть во всех регистрах сразу: из формы значение
	// попадает в Fields под именем метаданных, а присваивание в обработчике —
	// через Object.Set, то есть в нижнем регистре. После «Объект.Канал = …» в
	// карте лежат ОБА ключа, и сравнение по одному из них давало случайный ответ.
	fieldKeys := func(name string) []string {
		var keys []string
		low := strings.ToLower(name)
		for k := range obj.Fields {
			if strings.ToLower(k) == low {
				keys = append(keys, k)
			}
		}
		return keys
	}
	readOnly := readOnlyFormFields(form)
	// Редактируемый Флажок — единственный элемент, для которого отсутствие ключа
	// в POST означает «снято», а не «не передавалось» (контракт частичной записи,
	// см. restoreUnsubmittedFields). Перечитывание вернуло бы снятую галку
	// взведённой, а applyValues на клиенте поставил бы её обратно в DOM —
	// пользователь снимает галку, жмёт команду, галка молча возвращается.
	// ReadOnly-флажок в этот набор не попадает и перечитывается как результат.
	checkboxes := checkboxOmittedFields(form, entity)
	stale := func(f metadata.Field) bool {
		if checkboxes[strings.ToLower(f.Name)] {
			return false
		}
		// Нередактируемое поле перечитываем всегда. Оно приходит в POST, если
		// отрисовано как <input readonly> (в отличие от disabled-списка), но
		// пользователь его не вводит: это результат, и в форме он обязан быть
		// свежим. Иначе номер, присвоенный нумератором при записи, появлялся
		// на форме только после переоткрытия.
		if !readOnly[strings.ToLower(f.Name)] && formKeySubmitted(submitted, f.Name) {
			return false
		}
		keys := fieldKeys(f.Name)
		if len(keys) == 0 {
			return true
		}
		for _, k := range keys {
			was, existed := before[k]
			// Новый ключ или изменившееся значение — это присваивание внутри
			// обработчика. Оно может быть ещё не записано и обязано доехать
			// до формы как есть.
			if !existed || was != fmt.Sprintf("%v", obj.Fields[k]) {
				return false
			}
		}
		return true
	}

	need := false
	for _, f := range entity.Fields {
		if stale(f) {
			need = true
			break
		}
	}
	if !need {
		return
	}
	// Ошибку чтения глотаем: событие не должно падать из-за того, что запись
	// удалили параллельно — форма просто останется с прежними значениями.
	row, err := s.store.GetByID(ctx, entity.Name, obj.ID, entity)
	if err != nil || row == nil {
		return
	}
	for _, f := range entity.Fields {
		if !stale(f) {
			continue
		}
		value, ok := maskCIKeyValue(row, f.Name)
		if !ok {
			continue
		}
		fresh := normalizeRestoredValue(f, value)
		keys := fieldKeys(f.Name)
		if len(keys) == 0 {
			obj.Fields[f.Name] = fresh
			continue
		}
		// Обновляем все варианты регистра: сериализация ответа предпочитает
		// нижний, а оставленный устаревшим второй ключ снова дал бы «отставание».
		for _, k := range keys {
			obj.Fields[k] = fresh
		}
	}
}

// Возврат в форму строк табличной части, которые обработчик записал через общий
// модуль (issue #579).
//
// refreshFieldsWrittenByHandler выше перечитывает только реквизиты ШАПКИ. Строки
// ТЧ формируются из POST и не перечитывались, поэтому команда, меняющая строки
// через общий модуль (модуль читает документ, правит ТЧ, записывает), возвращала
// в форму ПРЕЖНИЕ строки — то же «отставание на шаг», что PR #573 закрыл для
// шапки.
//
// Трудность в том, чтобы отличить «модуль переписал ТЧ в базе» (перечитать) от
// «пользователь отредактировал строки в гриде и ещё не записал» (не затирать).
// Устойчивого ключа у строк POST нет, поэтому сравниваем состояния целиком, по
// значениям колонок ТЧ:
//
//  1. обработчик сам изменил строки в памяти (Объект.ТЧ.Добавить/…) — оставляем
//     как есть: это его намерение, оно может быть ещё не записано;
//  2. строки POST отличаются от строк в базе ДО обработчика — пользователь
//     правил грид, его правки не трогаем;
//  3. иначе (POST == база до обработчика, в памяти обработчик ТЧ не трогал) —
//     перечитываем из базы: если модуль переписал строки, приедут свежие; если
//     не трогал — база совпадёт с POST, и перечитывание ничего не меняет.
func (s *Server) refreshTablePartsWrittenByHandler(
	ctx context.Context,
	entity *metadata.Entity,
	obj *runtime.Object,
	tpPost map[string][]map[string]any,
	tpDBBefore map[string][]map[string]any,
) {
	if s == nil || s.store == nil || entity == nil || obj == nil || obj.ID == uuid.Nil {
		return
	}
	for _, tp := range entity.TableParts {
		cur := obj.TablePartRows[tp.Name]
		post := tpPost[tp.Name]
		// (1) строки изменил сам обработчик — не трогаем.
		if !tpRowsEqual(cur, post, tp) {
			continue
		}
		// (2) пользователь правил грид (POST ≠ база до обработчика) — не затираем.
		if !tpRowsEqual(post, tpDBBefore[tp.Name], tp) {
			continue
		}
		// (3) перечитываем строки из базы — их мог переписать модуль.
		fresh, err := s.store.GetTablePartRows(ctx, entity.Name, tp.Name, obj.ID, tp)
		if err != nil {
			continue
		}
		s.enrichTPRowsWithRefs(ctx, tp, fresh)
		if obj.TablePartRows == nil {
			obj.TablePartRows = map[string][]map[string]any{}
		}
		obj.TablePartRows[tp.Name] = fresh
	}
}

// tablePartRowsSnapshot делает глубокую копию строк ТЧ для сравнения «до/после»:
// обработчик может править строки на месте (formTpProxy), поэтому поверхностной
// копии мало.
func tablePartRowsSnapshot(src map[string][]map[string]any) map[string][]map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string][]map[string]any, len(src))
	for name, rows := range src {
		cp := make([]map[string]any, len(rows))
		for i, row := range rows {
			m := make(map[string]any, len(row))
			for k, v := range row {
				m[k] = v
			}
			cp[i] = m
		}
		out[name] = cp
	}
	return out
}

// tablePartRowsFromDB читает строки всех ТЧ записи из базы (сырые, без
// обогащения ссылками — сравнение идёт по значениям). Ошибку чтения ТЧ глотаем:
// команда не должна падать из-за неё, просто эту ТЧ не перечитаем.
func (s *Server) tablePartRowsFromDB(ctx context.Context, entity *metadata.Entity, id uuid.UUID) map[string][]map[string]any {
	out := make(map[string][]map[string]any, len(entity.TableParts))
	for _, tp := range entity.TableParts {
		rows, err := s.store.GetTablePartRows(ctx, entity.Name, tp.Name, id, tp)
		if err != nil {
			continue
		}
		out[tp.Name] = rows
	}
	return out
}

// tpRowsEqual сравнивает строки ТЧ по значениям объявленных колонок. Порядок
// строк значим (обе стороны упорядочены по номеру строки).
func tpRowsEqual(a, b []map[string]any, tp metadata.TablePart) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		for _, f := range tp.Fields {
			if tpCellNorm(f, tpCellValue(a[i], f.Name)) != tpCellNorm(f, tpCellValue(b[i], f.Name)) {
				return false
			}
		}
	}
	return true
}

func tpCellValue(row map[string]any, name string) any {
	if _, v, ok := lookupMapCI(row, name); ok {
		return v
	}
	return nil
}

// tpCellNorm приводит значение колонки к канонической строке, устойчивой к
// разнице представлений POST и базы: строки формы (Число → float64, из базы —
// TEXT/numeric), ссылки (*Ref из обогащения ↔ сырой UUID из базы) иначе давали
// бы ложное «пользователь правил грид», и строки ТЧ не перечитывались бы.
func tpCellNorm(f metadata.Field, v any) string {
	if ref, ok := v.(*interpreter.Ref); ok {
		if ref == nil {
			return ""
		}
		return ref.UUID
	}
	if f.RefEntity != "" {
		if idStr, _, ok := uuidFromValue(v); ok {
			return idStr
		}
	}
	switch f.Type {
	case metadata.FieldTypeNumber:
		switch t := v.(type) {
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case int64:
			return strconv.FormatInt(t, 10)
		case string:
			if x, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
				return strconv.FormatFloat(x, 'f', -1, 64)
			}
		}
	case metadata.FieldTypeBool:
		switch t := v.(type) {
		case bool:
			return boolCanon(t)
		case int64:
			// SQLite хранит булево как INTEGER (TypeBool → INTEGER), и драйвер
			// отдаёт int64. Без этой ветки значение уходило в общий Sprintf → "1"/"0"
			// и никогда не совпадало с "true"/"false" со стороны формы, поэтому
			// перечитывание ТЧ с булевым реквизитом не срабатывало на SQLite (#624).
			return boolCanon(t != 0)
		case int:
			return boolCanon(t != 0)
		case string:
			s := strings.ToLower(strings.TrimSpace(t))
			return boolCanon(s == "true" || s == "1" || s == "t")
		}
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// boolCanon — канон булева значения для сравнения «значение из БД против значения
// из формы»: одна и та же форма для bool, int64/int (SQLite) и строки.
func boolCanon(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// versionWrittenByHandler возвращает актуальную версию записи ТОЛЬКО когда
// объект записал сам обработчик текущего события (Объект.Записать() поднял
// версию, а форма держит прочитанную при отрисовке). Во всех прочих случаях —
// ноль: клиент оставляет _version, с которым форма была отрисована, и
// оптимистическая блокировка продолжает ловить чужую параллельную запись.
//
// Без этого гейта версия ехала клиенту после КАЖДОГО события формы (#608): между
// открытием и «Записать» пользователь жмёт любую кнопку — ответ отдаёт свежую
// версию из БД, клиент кладёт её в _version, и последующая проверка версии всегда
// совпадает, молча затирая правки того, кто сохранил запись параллельно.
func (s *Server) versionWrittenByHandler(ctx context.Context, entity *metadata.Entity, obj *runtime.Object, this *formObjectThis) int64 {
	if this == nil || !this.saved {
		return 0
	}
	return s.currentEntityVersion(ctx, entity, obj)
}

// currentEntityVersion возвращает версию записи после обработчика. Ноль — если
// записи ещё нет или версию не прочитать: тогда клиент оставляет прежнюю.
func (s *Server) currentEntityVersion(ctx context.Context, entity *metadata.Entity, obj *runtime.Object) int64 {
	if s == nil || s.store == nil || entity == nil || obj == nil || obj.ID == uuid.Nil {
		return 0
	}
	version, exists, err := s.store.EntityVersionExists(ctx, entity.Name, obj.ID)
	if err != nil || !exists {
		return 0
	}
	return version
}

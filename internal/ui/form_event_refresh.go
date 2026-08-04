package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

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

// readOnlyFormFields собирает имена полей сущности, отрисованных формой как
// нередактируемые (по последнему сегменту data_path, как checkboxOmittedFields).
func readOnlyFormFields(form *metadata.FormModule) map[string]bool {
	out := map[string]bool{}
	if form == nil {
		return out
	}
	form.Walk(func(el *metadata.FormElement) bool {
		if el != nil && el.ReadOnly && el.DataPath != "" {
			out[strings.ToLower(dpFieldName(el.DataPath))] = true
		}
		return true
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
	stale := func(f metadata.Field) bool {
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

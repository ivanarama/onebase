package ui

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ПослеОткрытия — отложенная загрузка данных формы (история клиента из 1С и
// т.п.): обработчик группы запускается клиентом ПОСЛЕ ответа ПриОткрытии
// отдельным запросом мимо общей очереди событий, пока на группе крутится
// индикатор. Форма открывается сразу, оператор работает, не дожидаясь ответа.
//
// Раз ответ приходит, когда пользователь и другие события уже могли изменить
// форму, он несёт только то, что изменил сам обработчик: реквизиты, значение
// которых отличается от присланного, и таблицы значений, чьи строки
// изменились. Состояния элементов, условное оформление и табличные части
// документа он не трогает — иначе откатил бы чужие правки.
const formEventAfterOpen = string(metadata.FormEventAfterOpen)

// deferredFormEvent — запрос отложенной загрузки (клиент шлёт _deferred=1 и
// событие ПослеОткрытия).
func deferredFormEvent(r *http.Request) bool {
	return r.FormValue("_deferred") == "1" && r.FormValue("_event") == formEventAfterOpen
}

// filterDeferredResponse оставляет в ответе отложенной загрузки только
// изменённое обработчиком.
func filterDeferredResponse(r *http.Request, resp *formEventResponse) {
	resp.Deferred = true
	for k, v := range resp.Values {
		posted, ok := postedValueCI(r, k)
		if ok && posted == deferredValueString(v) {
			delete(resp.Values, k)
		}
	}
	for name, rows := range resp.FormTables {
		if deferredRowsEqual(postedValueTableRows(r, name), rows) {
			delete(resp.FormTables, name)
		}
	}
	resp.TableParts = nil
	resp.ElementStates = nil
	resp.ConditionalCSS = ""
}

func postedValueCI(r *http.Request, key string) (string, bool) {
	for k, v := range r.PostForm {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0], true
		}
	}
	return "", false
}

func deferredValueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case map[string]any:
		// Ссылка уходит в ответ структурой {id, label}; браузер присылает id.
		if id, ok := x["id"]; ok {
			return fmt.Sprint(id)
		}
	}
	return fmt.Sprint(v)
}

// postedValueTableRows восстанавливает строки таблицы значений из присланных
// полей vt.<имя>.<строка>.<колонка>.
func postedValueTableRows(r *http.Request, name string) []map[string]string {
	prefix := "vt." + strings.ToLower(name) + "."
	byIndex := map[int]map[string]string{}
	for k, v := range r.PostForm {
		lk := strings.ToLower(k)
		if !strings.HasPrefix(lk, prefix) || len(v) == 0 {
			continue
		}
		rest := k[len(prefix):]
		dot := strings.IndexByte(rest, '.')
		if dot < 0 {
			continue
		}
		idx, err := strconv.Atoi(rest[:dot])
		if err != nil {
			continue
		}
		if byIndex[idx] == nil {
			byIndex[idx] = map[string]string{}
		}
		byIndex[idx][strings.ToLower(rest[dot+1:])] = v[0]
	}
	idx := make([]int, 0, len(byIndex))
	for i := range byIndex {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]map[string]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, byIndex[i])
	}
	return out
}

func deferredRowsEqual(posted []map[string]string, rows []map[string]any) bool {
	if len(posted) != len(rows) {
		return false
	}
	for i, row := range rows {
		for k, v := range row {
			if strings.HasPrefix(k, "_") {
				continue // служебные ключи строки (оформление) браузер не шлёт
			}
			if posted[i][strings.ToLower(k)] != deferredValueString(v) {
				return false
			}
		}
	}
	return true
}

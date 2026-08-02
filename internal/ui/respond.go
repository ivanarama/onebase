package ui

import (
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

// Запись в уже начатый HTTP-ответ.
//
// К моменту этих вызовов заголовки отправлены (а иногда и часть тела), поэтому
// вернуть пользователю другой статус нельзя — план 109 предписывает для такого
// случая зафиксировать сбой структурным логом и прекратить запись. Решение
// принято здесь один раз, вместо того чтобы повторяться в каждом обработчике.
//
// Уровни разные не для красоты, а по природе сбоя:
//   - JSON-ответ обычно не дописывается, потому что клиент отсоединился —
//     штатная ситуация, Debug, иначе журнал зальёт при каждом закрытии вкладки;
//   - сбой шаблона чаще означает ошибку в самом шаблоне (нет поля, nil-карта),
//     и пользователь получает обрезанную страницу — это Warn, такое надо видеть.

func uiLog() *slog.Logger { return oblog.Component("ui") }

// respondJSON дописывает значение в ответ через уже созданный энкодер.
func respondJSON(enc *json.Encoder, v any) {
	if err := enc.Encode(v); err != nil {
		uiLog().Debug("не удалось записать JSON-ответ", "err", err)
	}
}

// respondJSONTo кодирует значение прямо в ответ (когда энкодер не переиспользуется).
func respondJSONTo(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		uiLog().Debug("не удалось записать JSON-ответ", "err", err)
	}
}

// renderTemplate отрисовывает именованный шаблон в уже начатый ответ.
func renderTemplate(w http.ResponseWriter, t *template.Template, name string, data any) {
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		uiLog().Warn("не удалось отрисовать шаблон", "template", name, "err", err)
	}
}

// renderAdminTemplate отрисовывает шаблон админки в уже начатый ответ.
func renderAdminTemplate(w http.ResponseWriter, name string, data any) {
	renderTemplate(w, adminTmpl, name, data)
}

package auth

import (
	"encoding/json"
	"html/template"
	"net/http"
)

// Запись в уже начатый ответ страницы входа.
//
// Заголовки к этому моменту отправлены, вернуть другой статус нельзя — как и в
// internal/ui, остаётся зафиксировать сбой и прекратить запись. Уровни те же и
// по той же причине: JSON обычно не дописывается из-за отсоединившегося
// клиента (Debug), а сбой шаблона означает ошибку в самом шаблоне и обрезанную
// страницу входа (Warn) — такое надо видеть, страница входа одна на всех.

func respondJSONTo(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		authLog().Debug("не удалось записать JSON-ответ", "err", err)
	}
}

func renderTemplate(w http.ResponseWriter, t *template.Template, data any) {
	if err := t.Execute(w, data); err != nil {
		authLog().Warn("не удалось отрисовать страницу входа", "err", err)
	}
}

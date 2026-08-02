package ui

import (
	"encoding/json"
	"html/template"
	"io"
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

// writeBody дописывает тело уже начатого ответа: страницу, фрагмент, картинку.
// Обрыв виден пользователю сразу (пустая страница, битая картинка), причина
// почти всегда внешняя — закрыли вкладку. Debug, иначе журнал зальёт.
// G705 (taint-анализ gosec) видит здесь единый сток для HTML-ответов и не видит
// санитизацию: путь, из-за которого правило срабатывает, — печатная форма
// (проверено побайтовым отключением вызовов), а она экранируется внутри
// internal/sheet собственными escapeHTML/richtext.Sanitize/csssafe и allowlist
// classifyPicture. Стандартных html.EscapeString/template там нет, поэтому
// gosec их не распознаёт. То же решение и по той же причине принято в
// internal/launcher/respond.go.
func writeBody(w http.ResponseWriter, b []byte) {
	if _, err := w.Write(b); err != nil { //nolint:gosec // G705: HTML печатной формы санитизируется в internal/sheet — gosec этих санитайзеров не знает
		uiLog().Debug("не удалось записать тело ответа", "err", err)
	}
}

// writeDownload отдаёт содержимое, которое пользователь сохраняет файлом: PDF
// печатной формы, выгрузку xlsx, бандл внешней формы.
//
// Уровень выше, чем у writeBody, по той же причине, что и в конфигураторе:
// обрыв здесь НЕ виден. Получается внешне целый, но усечённый файл — отчёт,
// который откроется с половиной строк, и это спишут на данные, а не на обрыв
// загрузки.
// G705 здесь ложное по контракту: все вызывающие отдают вложение
// (Content-Disposition) с не-HTML типом — application/pdf, xlsx, x-yaml, а в
// export_jobs — один из этих двух. HTML-страницу через эту функцию не отдают, и
// браузер её содержимое не исполняет.
func writeDownload(w http.ResponseWriter, name string, b []byte) {
	if _, err := w.Write(b); err != nil { //nolint:gosec // G705: ответ не HTML-страница — вложение с типом pdf/xlsx/yaml
		uiLog().Warn("ответ с файлом оборван — содержимое у пользователя усечено",
			"file", name, "size", len(b), "err", err)
	}
}

// closeRead закрывает читающую сторону: загруженный файл, тело запроса,
// открытое вложение. Данные уже прочитаны, ошибка вторична — Debug.
func closeRead(what string, c io.Closer) {
	if err := c.Close(); err != nil {
		uiLog().Debug("не удалось закрыть "+what, "err", err)
	}
}

// bestEffort фиксирует ошибку уборки, не меняя исход операции.
func bestEffort(what string, err error) {
	if err != nil {
		uiLog().Debug("не удалось "+what, "err", err)
	}
}

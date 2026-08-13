package launcher

// Одноразовые сообщения списка баз.
//
// Кнопки тулбара («Остановить», «Стоп всё», перемещение) отправляются
// навигацией, поэтому ответ обработчика становится самой страницей. Ошибка,
// отданная текстом (http.Error), превращала окно лаунчера в тупик: в нативном
// окне нет ни адресной строки, ни кнопки «Назад», и вернуться к списку баз
// было нечем. Теперь обработчик кладёт сообщение сюда, редиректит на список, а
// страница показывает его баннером и сразу забывает.
//
// Текст живёт на сервере, в адресе — только одноразовый ключ: показывать в
// баннере произвольную строку из URL нельзя, любая локальная страница могла бы
// навигацией нарисовать в окне лаунчера чужой текст от его имени.

import (
	"net/http"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/processcontrol"
)

const (
	flashError   = "error"
	flashWarning = "warning"

	// flashTTL: сообщение нужно ровно на один редирект. Всё, что не забрали
	// (закрыли окно, оборвали навигацию), устаревает и вычищается.
	flashTTL = 2 * time.Minute
	// maxFlashes ограничивает память при потоке неудачных операций.
	maxFlashes = 32
	// maxFlashLen: баннер — не место для мегабайтной ошибки.
	maxFlashLen = 2000
)

type flashMessage struct {
	kind    string
	text    string
	created time.Time
}

// putFlash сохраняет сообщение и возвращает ключ для адреса редиректа.
// Пустой ключ означает «показывать нечего» — вызывающий редиректит как обычно.
func (h *handler) putFlash(kind, text string) string {
	if text == "" {
		return ""
	}
	if runes := []rune(text); len(runes) > maxFlashLen {
		text = string(runes[:maxFlashLen]) + "…"
	}
	key, err := processcontrol.NewNonce()
	if err != nil {
		respondLog().Warn("не удалось создать ключ сообщения для списка баз", "err", err)
		return ""
	}
	now := time.Now()
	h.flashMu.Lock()
	defer h.flashMu.Unlock()
	if h.flashes == nil {
		h.flashes = make(map[string]flashMessage, 4)
	}
	for k, v := range h.flashes {
		if now.Sub(v.created) > flashTTL {
			delete(h.flashes, k)
		}
	}
	if len(h.flashes) >= maxFlashes {
		oldest, oldestAt := "", now
		for k, v := range h.flashes {
			if oldest == "" || v.created.Before(oldestAt) {
				oldest, oldestAt = k, v.created
			}
		}
		delete(h.flashes, oldest)
	}
	h.flashes[key] = flashMessage{kind: kind, text: text, created: now}
	return key
}

// takeFlash забирает сообщение по ключу: показать его надо один раз, иначе
// обновление страницы (F5) повторяло бы старую ошибку.
func (h *handler) takeFlash(key string) (flashMessage, bool) {
	if key == "" {
		return flashMessage{}, false
	}
	h.flashMu.Lock()
	defer h.flashMu.Unlock()
	msg, ok := h.flashes[key]
	if !ok {
		return flashMessage{}, false
	}
	delete(h.flashes, key)
	if time.Since(msg.created) > flashTTL {
		return flashMessage{}, false
	}
	return msg, true
}

// redirectWithFlash возвращает пользователя к списку баз с сообщением о
// результате операции.
func (h *handler) redirectWithFlash(w http.ResponseWriter, r *http.Request, target, kind, text string) {
	if key := h.putFlash(kind, text); key != "" {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + "flash=" + key
	}
	http.Redirect(w, r, target, http.StatusFound)
}

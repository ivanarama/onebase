package ui

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// uiBootID — метка запуска процесса. Страница несёт её в
// <meta name="ob-boot">, ответы интерфейса — в заголовке X-OB-Boot. Форма,
// открытая до перезапуска сервера (выпуск, обновление), по несовпадению меток
// узнаёт, что её серверное состояние (контексты раскрытия, ожидания) пропало,
// и предлагает перезагрузиться вместо того, чтобы молча «зависнуть».
var uiBootID = newUIBootID()

func newUIBootID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// markFreshPage — страница интерфейса не кэшируется браузером: «Назад» и
// кэш «Назад/Вперёд» (bfcache) не поднимают старый DOM со старыми скрытыми
// полями, версией объекта и уже недействительными кнопками. private — данные
// страницы (в том числе ПДн) принадлежат пользователю. Явно выставленный
// обработчиком Cache-Control не перетирается.
func markFreshPage(w http.ResponseWriter) {
	h := w.Header()
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "private, no-store")
	}
	h.Set("X-OB-Boot", uiBootID)
}

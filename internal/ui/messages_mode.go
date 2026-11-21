package ui

import (
	"html/template"
	"strconv"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Режим показа сообщений интерфейса (config/home_page.yaml: messages,
// message_seconds). По умолчанию — как раньше: плашки событий формы вверху
// справа и нижняя панель «Сообщения». messages: popup убирает панель: всё, что
// приходит через /ui/messages (фоновые задания, другие окна, запись формы),
// показывается той же всплывающей плашкой, без дублей с ответом события.
// message_seconds — сколько секунд держать обычную плашку; ошибки висят до
// крестика.

// messagesModeData — режим для общего head, nil — всё по умолчанию.
func (s *Server) messagesModeData() *metadata.HomePage {
	if s == nil || s.reg == nil {
		return nil
	}
	hp := s.reg.HomePage()
	if hp == nil || (hp.Messages == "" && hp.MessageSeconds == 0) {
		return nil
	}
	return hp
}

// messagesMetaHTML — <meta name="ob-messages"> для ui.js и managed.js. Данные
// шаблона бывают не картой, поэтому значение берётся из карты.
func messagesMetaHTML(data any) template.HTML {
	m, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	hp, _ := m["MessagesMode"].(*metadata.HomePage)
	if hp == nil {
		return ""
	}
	mode := hp.Messages
	if mode == "" {
		mode = metadata.MessagesPanel
	}
	return template.HTML(`<meta name="ob-messages" content="` + mode + `" data-seconds="` + //nolint:gosec // G203: режим проверен загрузчиком, секунды — целое
		strconv.Itoa(hp.MessageSecondsOrDefault()) + `">`)
}

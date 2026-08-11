package launcher

// Поведение при закрытии окна информационных баз.
//
// Запущенные базы намеренно переживают закрытие лаунчера (см. Server.Close):
// лаунчер — окно запуска, а не владелец сеансов. Но раньше это происходило
// молча: пользователь закрывал окно крестиком и не знал, что серверы баз
// остались работать — держат порт, файл SQLite, сеансы пользователей и крутят
// регламентные задания. Теперь при закрытии с живыми базами лаунчер спрашивает,
// что с ними делать, и умеет запомнить ответ.

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

// Значения настройки LauncherSettings.OnClose.
const (
	OnCloseAsk        = "ask"        // спросить (по умолчанию)
	OnCloseBackground = "background" // оставить базы работать в фоне
	OnCloseStop       = "stop"       // остановить все базы
)

// maxDialogBases — сколько баз перечислять в диалоге поимённо: длинный список
// в системном MessageBox не листается и вытесняет сами варианты ответа.
const maxDialogBases = 10

func normalizeOnClose(v string) string {
	switch v {
	case OnCloseBackground, OnCloseStop:
		return v
	default:
		return OnCloseAsk
	}
}

// closePlan — что делать при попытке закрыть окно лаунчера.
type closePlan int

const (
	planKeepRunning closePlan = iota // закрыть окно, базы оставить работать
	planAsk                          // спросить пользователя
	planStopAll                      // остановить базы и закрыть окно
)

// planForClose: спрашиваем, только когда есть что оставлять в фоне — иначе
// диалог был бы пустой формальностью на каждом выходе.
func planForClose(policy string, running int) closePlan {
	if running == 0 {
		return planKeepRunning
	}
	switch normalizeOnClose(policy) {
	case OnCloseBackground:
		return planKeepRunning
	case OnCloseStop:
		return planStopAll
	default:
		return planAsk
	}
}

// RunningBase — работающая база для диалога закрытия.
type RunningBase struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

// CloseCoordinator — то, что окно лаунчера спрашивает при закрытии. Реализует
// *Server; окну (window_webview.go) он передаётся, чтобы перехват системного
// крестика не тянул за собой весь handler.
type CloseCoordinator interface {
	// RunningBases возвращает работающие базы (для текста диалога).
	RunningBases() []RunningBase
	// OnClosePolicy возвращает сохранённую настройку (ask/background/stop).
	OnClosePolicy() string
	// StopAllBases останавливает все базы. Может занять секунды — вызывать
	// не из потока окна.
	StopAllBases()
}

// windowLang запоминает язык последней отрисованной страницы лаунчера:
// нативный диалог закрытия рисуется вне HTTP-запроса, и Accept-Language
// взять неоткуда.
var windowLang atomic.Value // string

func rememberLang(lang string) {
	if lang != "" {
		windowLang.Store(lang)
	}
}

func currentLang() string {
	if v, ok := windowLang.Load().(string); ok && v != "" {
		return v
	}
	return "ru"
}

// closeDialogText собирает текст системного диалога. Кнопки MessageBox
// подписаны системой («Да»/«Нет»/«Отмена»), поэтому что именно делает каждая —
// расшифровано в самом тексте.
func closeDialogText(lang string, running []RunningBase) string {
	var b strings.Builder
	b.WriteString(tr(lang, "Работают информационные базы:"))
	b.WriteString("\n")
	for i, rb := range running {
		if i == maxDialogBases {
			fmt.Fprintf(&b, "  %s %d\n", tr(lang, "и ещё баз:"), len(running)-maxDialogBases)
			break
		}
		fmt.Fprintf(&b, "  • %s (%s %d)\n", rb.Name, tr(lang, "порт"), rb.Port)
	}
	b.WriteString("\n")
	b.WriteString(tr(lang, "Продолжить их работу в фоновом режиме?"))
	b.WriteString("\n\n")
	b.WriteString(tr(lang, "Да — базы продолжат работать: регламентные задания, обмены и подключённые пользователи не пострадают. Окно запуска закроется — откройте его снова, чтобы остановить базы."))
	b.WriteString("\n\n")
	b.WriteString(tr(lang, "Нет — остановить все базы: открытые окна Предприятия и подключённые пользователи потеряют связь."))
	b.WriteString("\n\n")
	b.WriteString(tr(lang, "Отмена — не закрывать окно."))
	return b.String()
}

// runningBases — работающие базы в порядке реестра. Статусы берём тем же
// baseStatuses, что и список: он пробует базы параллельно и с общим TTL, так что
// вопрос при закрытии не превращается в серию /health по всем базам подряд.
func (h *handler) runningBases() []RunningBase {
	bases, err := h.store.List()
	if err != nil {
		respondLog().Warn("не удалось прочитать реестр баз для диалога закрытия", "err", err)
		return nil
	}
	statuses := h.baseStatuses(bases)
	var out []RunningBase
	for _, b := range bases {
		if statuses[b.ID].running {
			out = append(out, RunningBase{Name: b.Name, Port: b.Port})
		}
	}
	return out
}

func (h *handler) onClosePolicy() string {
	st, err := h.store.Settings()
	if err != nil {
		return OnCloseAsk
	}
	return normalizeOnClose(st.OnClose)
}

// stopAllBases — общий «Стоп всё»: гасит и отслеживаемые процессы, и живые
// чужие (усыновлённые) базы по портам реестра.
func (h *handler) stopAllBases() {
	var ports []int
	if bases, err := h.store.List(); err == nil {
		for _, b := range bases {
			ports = append(ports, b.Port)
		}
	}
	h.runner.StopAll(ports)
	h.clearStatus() // все базы остановлены — сбрасываем весь кэш статусов
}

// closeInfo отдаёт клиенту состояние для диалога закрытия. Список берётся на
// момент закрытия, а не из отрисованной страницы: базу могли запустить или
// остановить из другого окна.
func (h *handler) closeInfo(w http.ResponseWriter, r *http.Request) {
	running := h.runningBases()
	if running == nil {
		running = []RunningBase{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running": running,
		"policy":  h.onClosePolicy(),
	})
}

// setClosePolicy сохраняет выбор «больше не спрашивать».
func (h *handler) setClosePolicy(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	policy := r.FormValue("policy")
	// Молча подменять непонятное значение на «спрашивать» нельзя: клиент решит,
	// что выбор сохранён, а он потерян.
	if policy != OnCloseAsk && policy != OnCloseBackground && policy != OnCloseStop {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "неизвестное значение policy: " + policy})
		return
	}
	if err := h.store.SetOnClose(policy); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RunningBases реализует CloseCoordinator.
func (s *Server) RunningBases() []RunningBase { return s.h.runningBases() }

// OnClosePolicy реализует CloseCoordinator.
func (s *Server) OnClosePolicy() string { return s.h.onClosePolicy() }

// StopAllBases реализует CloseCoordinator.
func (s *Server) StopAllBases() { s.h.stopAllBases() }

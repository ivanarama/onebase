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
	"sync"
	"sync/atomic"
	"time"
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
	switch normalizeOnClose(policy) {
	case OnCloseBackground:
		return planKeepRunning
	case OnCloseStop:
		return planStopAll
	default:
		if running == 0 {
			return planKeepRunning
		}
		return planAsk
	}
}

// RunningBase — работающая база для диалога закрытия.
type RunningBase struct {
	Name         string `json:"name"`
	Port         int    `json:"port"`
	Controllable bool   `json:"controllable"`
}

// CloseCoordinator — то, что окно лаунчера спрашивает при закрытии. Реализует
// *Server; окну (window_webview.go) он передаётся, чтобы перехват системного
// крестика не тянул за собой весь handler.
type CloseCoordinator interface {
	// CloseState возвращает один согласованный snapshot для решения о закрытии.
	CloseState() ([]RunningBase, string, error)
	// StopAllBases останавливает все базы и подтверждает результат. Может занять
	// секунды — вызывать не из потока окна. Первым значением возвращает базы,
	// которые остались работать: их порт занят неподтверждённым процессом.
	StopAllBases() ([]RunningBase, error)
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
		name := strings.Join(strings.Fields(rb.Name), " ")
		runes := []rune(name)
		if len(runes) > 80 {
			name = string(runes[:79]) + "…"
		}
		blocked := ""
		if !rb.Controllable {
			blocked = " — " + tr(lang, "порт занят; автоматическая остановка недоступна")
		}
		fmt.Fprintf(&b, "  • %s (%s %d)%s\n", name, tr(lang, "порт"), rb.Port, blocked)
	}
	for _, rb := range running {
		if !rb.Controllable {
			b.WriteString("\n")
			b.WriteString(tr(lang, "Один или несколько портов заняты неподтверждённым процессом: такие базы лаунчер не останавливает, они продолжат работать при любом ответе."))
			b.WriteString("\n")
			break
		}
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

// stoppableBases — сколько из перечисленных баз лаунчер действительно умеет
// остановить. Вопрос «оставить работать в фоне?» имеет смысл только про них:
// чужой процесс на зарегистрированном порту продолжит работать при любом
// ответе, и спрашивать из-за него — значит показывать диалог, у которого нет
// работающего варианта «Нет».
func stoppableBases(running []RunningBase) int {
	n := 0
	for _, rb := range running {
		if rb.Controllable {
			n++
		}
	}
	return n
}

// skippedBasesText — предупреждение о базах, которых остановка не коснулась.
// Формулировка одинаково верна и когда база работает без подтверждённой
// идентичности, и когда её порт занят посторонней программой: лаунчер знает
// только, что владельца порта он не опознал.
func skippedBasesText(lang string, skipped []RunningBase) string {
	var b strings.Builder
	b.WriteString(tr(lang, "Порт занят процессом, принадлежность которого лаунчер не подтвердил, — эти базы он не трогал (при необходимости остановите процесс вручную):"))
	for i, rb := range skipped {
		if i == maxDialogBases {
			fmt.Fprintf(&b, "\n  %s %d", tr(lang, "и ещё баз:"), len(skipped)-maxDialogBases)
			break
		}
		fmt.Fprintf(&b, "\n  • %s (%s %d)", strings.Join(strings.Fields(rb.Name), " "), tr(lang, "порт"), rb.Port)
	}
	return b.String()
}

// closeState берёт список и policy одним чтением Store, а живость проверяет
// заново, параллельно и без app.yaml/конфигурационной БД. Поэтому результат
// действительно относится к моменту закрытия и имеет общий bounded timeout.
func (h *handler) closeState() ([]RunningBase, string, error) {
	bases, settings, err := h.store.Snapshot()
	if err != nil {
		return nil, OnCloseAsk, fmt.Errorf("прочитать реестр баз: %w", err)
	}
	statuses := make([]BaseRuntimeStatus, len(bases))
	var wg sync.WaitGroup
	for i, base := range bases {
		i, base := i, base
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = h.runner.RuntimeStatus(base)
		}()
	}
	wg.Wait()
	out := make([]RunningBase, 0, len(bases))
	for i, base := range bases {
		if statuses[i].Running || statuses[i].Occupied {
			out = append(out, RunningBase{Name: base.Name, Port: base.Port,
				Controllable: statuses[i].Controllable})
		}
	}
	return out, normalizeOnClose(settings.OnClose), nil
}

func (h *handler) onClosePolicy() string {
	st, err := h.store.Settings()
	if err != nil {
		return OnCloseAsk
	}
	return normalizeOnClose(st.OnClose)
}

// stopAllBases останавливает только доказуемо принадлежащие onebase процессы:
// tracked — по os.Process, усыновлённые — через token-protected control API.
// Номер зарегистрированного порта сам по себе больше не даёт права на kill.
//
// Первым значением возвращаются базы, чей порт занят неподтверждённым
// процессом. Это не ошибка операции: остановить их лаунчер не вправе, но и
// запрещать из-за них закрытие окна нельзя — иначе выйти из лаунчера
// невозможно, пока порт занят.
func (h *handler) stopAllBases(holdStarts bool) ([]RunningBase, error) {
	bases, _, err := h.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("прочитать реестр баз перед остановкой: %w", err)
	}
	skipped, err := h.runner.StopAll(bases, holdStarts)
	if err != nil {
		return skipped, err
	}
	h.clearStatus()
	running, _, err := h.closeState()
	if err != nil {
		if holdStarts {
			h.runner.AllowStarts()
		}
		return skipped, fmt.Errorf("проверить результат остановки: %w", err)
	}
	// Свежая проверка вернее preflight: пересобираем список пропущенных по ней.
	// Невыполненной операция считается только тогда, когда база, которой мы
	// управляем, всё ещё жива — за чужой процесс на порту лаунчер не отвечает.
	skipped = nil
	var stillRunning []string
	for _, base := range running {
		if base.Controllable {
			stillRunning = append(stillRunning, base.Name)
			continue
		}
		skipped = append(skipped, base)
	}
	if len(stillRunning) != 0 {
		if holdStarts {
			h.runner.AllowStarts()
		}
		return skipped, fmt.Errorf("не остановлены базы: %s", strings.Join(stillRunning, ", "))
	}
	return skipped, nil
}

// closeInfo отдаёт клиенту состояние для диалога закрытия. Список берётся на
// момент закрытия, а не из отрисованной страницы: базу могли запустить или
// остановить из другого окна.
func (h *handler) closeInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	running, policy, err := h.closeState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running": running,
		"policy":  policy,
	})
}

// closeStop — JSON-вариант «Стоп всё» для close state-machine. Успех означает,
// что повторная свежая проверка не нашла работающих баз. Только после этого
// сервер просит окно завершиться; клиенту не нужно гоняться отдельным /quit.
func (h *handler) closeStop(w http.ResponseWriter, r *http.Request) {
	skipped, err := h.stopAllBases(true)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	resp := map[string]any{"ok": true}
	if len(skipped) != 0 {
		resp["warning"] = skippedBasesText(resolveLang(r), skipped)
	}
	writeJSON(w, http.StatusOK, resp)
	if h.quitFn != nil {
		quit := h.quitFn
		go func() {
			// Дать JSON-ответу уйти до Server.Close: иначе fetch увидит network
			// error ровно после успешной, подтверждённой остановки.
			time.Sleep(100 * time.Millisecond)
			quit()
		}()
	}
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

// CloseState реализует CloseCoordinator.
func (s *Server) CloseState() ([]RunningBase, string, error) { return s.h.closeState() }

// StopAllBases реализует CloseCoordinator.
func (s *Server) StopAllBases() ([]RunningBase, error) { return s.h.stopAllBases(true) }

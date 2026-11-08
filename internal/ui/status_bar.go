package ui

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Строка состояния внизу каждой страницы (status_bar в config/home_page.yaml).
//
// Страница несёт только признак строки и период обновления (meta
// ob-status-bar в общем head); содержимое ui.js берёт отсюда же:
//   - GET  /ui/_status-bar — текущее состояние;
//   - POST /ui/_status-bar — нажатие кнопки (button=<имя>), в ответ — новое
//     состояние и сообщение обработчика.
//
// Содержимое считает функция конфигурации под текущим пользователем — с его
// ролями и доступом к строкам, как любой код формы. Время одного вызова
// ограничено: строку опрашивают раз в несколько секунд с каждой открытой
// страницы, и зависший обработчик не должен копить запросы.
const statusBarCallTimeout = 5 * time.Second

// statusBarFor — строка для пользователя запроса или nil: не настроена, не
// вошёл, роль не подходит.
func (s *Server) statusBarFor(r *http.Request) *metadata.StatusBar {
	if s == nil || s.reg == nil || r == nil {
		return nil
	}
	hp := s.reg.HomePage()
	if hp == nil || hp.StatusBar == nil {
		return nil
	}
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return nil
	}
	roles := make([]string, 0, len(u.Roles))
	for _, role := range u.Roles {
		if role != nil {
			roles = append(roles, role.Name)
		}
	}
	if !hp.StatusBar.ForRoles(roles) {
		return nil
	}
	return hp.StatusBar
}

// addStatusBarData кладёт строку в данные страницы для общего head.
func (s *Server) addStatusBarData(r *http.Request, data map[string]any) {
	if _, ok := data["StatusBar"]; ok {
		return
	}
	if bar := s.statusBarFor(r); bar != nil {
		data["StatusBar"] = bar
	}
}

// statusBarState — ответ /ui/_status-bar.
type statusBarState struct {
	Visible  bool              `json:"visible"`
	Left     string            `json:"left,omitempty"`
	Right    string            `json:"right,omitempty"`
	Buttons  []statusBarButton `json:"buttons,omitempty"`
	Message  string            `json:"message,omitempty"`
	Error    string            `json:"error,omitempty"`
	Interval int               `json:"interval"`
}

type statusBarButton struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

func (s *Server) statusBarGet(w http.ResponseWriter, r *http.Request) {
	bar := s.statusBarFor(r)
	if bar == nil {
		writeStatusBar(w, statusBarState{Interval: metadata.StatusBarDefaultInterval})
		return
	}
	writeStatusBar(w, s.statusBarCompute(r, bar))
}

func (s *Server) statusBarPress(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	bar := s.statusBarFor(r)
	if bar == nil {
		http.Error(w, "строка состояния недоступна", http.StatusNotFound)
		return
	}
	name := strings.TrimSpace(r.FormValue("button"))
	var button *metadata.StatusBarButton
	for i := range bar.Buttons {
		if strings.EqualFold(bar.Buttons[i].Name, name) {
			button = &bar.Buttons[i]
			break
		}
	}
	if button == nil {
		http.Error(w, "кнопка не найдена", http.StatusNotFound)
		return
	}
	// Кнопка должна быть показана этому пользователю сейчас: функция строки
	// могла её спрятать (например, «На линии», когда оператор уже на линии).
	state := s.statusBarCompute(r, bar)
	shown := false
	for _, b := range state.Buttons {
		if strings.EqualFold(b.Name, button.Name) {
			shown = true
		}
	}
	if !state.Visible || !shown {
		http.Error(w, "кнопка сейчас недоступна", http.StatusConflict)
		return
	}
	result, err := s.statusBarCall(r, bar.Action, []any{button.Name})
	after := s.statusBarCompute(r, bar)
	if err != nil {
		after.Error = interpreter.FormatUserError(err)
	} else if msg, ok := result.(string); ok {
		after.Message = msg
	}
	writeStatusBar(w, after)
}

// statusBarCompute вызывает функцию строки и разбирает её Структуру.
// Ошибка функции не прячет строку: оператор видит, что сведения не получены,
// а не пустое место, которое легко принять за «звонка нет».
func (s *Server) statusBarCompute(r *http.Request, bar *metadata.StatusBar) statusBarState {
	state := statusBarState{Interval: bar.IntervalSeconds()}
	result, err := s.statusBarCall(r, bar.Source, nil)
	if err != nil {
		state.Visible = true
		state.Error = interpreter.FormatUserError(err)
		return state
	}
	if result == nil {
		return state // Неопределено — строки у этого пользователя нет
	}
	fields := map[string]any{}
	if data, mErr := interpreter.MarshalDSLValue(result); mErr == nil {
		var raw map[string]any
		if json.Unmarshal(data, &raw) == nil {
			for k, v := range raw {
				fields[strings.ToLower(k)] = v
			}
		}
	}
	state.Visible = true
	state.Left = statusBarText(fields["слева"])
	state.Right = statusBarText(fields["справа"])
	// Кнопки: без поля — все объявленные, с массивом — только перечисленные,
	// в порядке объявления.
	allowed, filter := map[string]bool{}, false
	if list, ok := fields["кнопки"].([]any); ok {
		filter = true
		for _, item := range list {
			if name, ok := item.(string); ok {
				allowed[strings.ToLower(strings.TrimSpace(name))] = true
			}
		}
	}
	for _, b := range bar.Buttons {
		if filter && !allowed[strings.ToLower(b.Name)] {
			continue
		}
		title := b.Title
		if title == "" {
			title = b.Name
		}
		state.Buttons = append(state.Buttons, statusBarButton{Name: b.Name, Title: title})
	}
	return state
}

func statusBarText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		data, _ := json.Marshal(t)
		return string(data)
	}
}

// statusBarCall вызывает «Модуль.Процедура» конфигурации под пользователем
// запроса с ограничением времени.
func (s *Server) statusBarCall(r *http.Request, ref string, args []any) (any, error) {
	module, name, ok := metadata.SplitStatusBarProc(ref)
	if !ok || s.interp == nil {
		return nil, errStatusBarProc(ref)
	}
	proc := s.reg.GetModuleNamespacedProc(module, name)
	if proc == nil {
		return nil, errStatusBarProc(ref)
	}
	ctx, cancel := context.WithTimeout(r.Context(), statusBarCallTimeout)
	defer cancel()
	var msgs []string
	mc := runtime.NewMovementsCollector("status_bar", uuid.Nil)
	vars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &msgs)
	defer rollbackDSLExecution(txState)
	result, err := s.interp.CallSandboxed(proc, nil, args,
		interpreter.SandboxProfile{Context: ctx, MaxWallClock: statusBarCallTimeout}, vars)
	return result, finishDSLExecution(txState, err)
}

type errStatusBarProc string

func (e errStatusBarProc) Error() string {
	return "строка состояния: процедура «" + string(e) + "» не найдена в общих модулях"
}

func writeStatusBar(w http.ResponseWriter, state statusBarState) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(state)
}

// statusBarMetaHTML — признак строки для общего head. Данные шаблона бывают
// не картой (отдельные страницы передают свои структуры), поэтому признак
// берётся из карты, а не обращением .StatusBar, которое уронило бы такой
// шаблон.
func statusBarMetaHTML(data any) template.HTML {
	m, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	bar, _ := m["StatusBar"].(*metadata.StatusBar)
	if bar == nil {
		return ""
	}
	return template.HTML(`<meta name="ob-status-bar" content="` + strconv.Itoa(bar.IntervalSeconds()) + `">`) //nolint:gosec // G203: только целое число 1…60
}

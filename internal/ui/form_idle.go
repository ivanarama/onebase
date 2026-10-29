package ui

import (
	"net/http"

	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Обработчик ожидания управляемой формы — ПодключитьОбработчикОжидания /
// ОтключитьОбработчикОжидания 1С.
//
// Таймер живёт в браузере: пока форма открыта, managed.js раз в N секунд
// шлёт обычное событие формы ОбработкаОжидания с текущими значениями формы, и
// ответ применяется как ответ любого события. Форма закрылась или страница
// перезагрузилась — таймера нет; после перезагрузки его снова подключает
// ПриОткрытии.
//
// Процедуру тика называет браузер, а вызвать он может только ту, которую
// подключил сам модуль формы: подключение возвращает подпись (HMAC ключом
// процесса) над «пользователь, форма, процедура», и тик без верной подписи
// отклоняется. Иначе событие тика позволило бы вызвать из браузера любую
// процедуру модуля формы. Ключ живёт до перезапуска сервера: после него тик
// отклоняется, браузер снимает таймер, а перезагрузка формы подключит заново.

// formIdleEvent — событие тика. В metadata его нет намеренно: объявлять его в
// events: формы нельзя и незачем — процедуру задаёт сам модуль подключением.
const formIdleEvent = "ОбработкаОжидания"

// formIdleMinInterval — меньше секунды не опрашиваем: каждый тик — запрос к
// серверу с полным состоянием формы.
const formIdleMinInterval = 1.0

// idleCommand — команда браузеру из ответа события: подключить (с подписью)
// или отключить таймер процедуры.
type idleCommand struct {
	Proc     string  `json:"proc"`
	Interval float64 `json:"interval,omitempty"`
	Once     bool    `json:"once,omitempty"`
	Token    string  `json:"token,omitempty"`
	Off      bool    `json:"off,omitempty"`
}

var errFormIdleToken = errors.New("обработчик ожидания устарел — форма подключит его заново при открытии")

func (s *Server) formIdleKey() []byte {
	s.formIdleOnce.Do(func() {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic(fmt.Sprintf("form idle key: %v", err))
		}
		s.formIdleSecret = key
	})
	return s.formIdleSecret
}

// formIdleToken — подпись «пользователь, вид+владелец формы, форма, процедура».
func (s *Server) formIdleToken(login, owner, form, proc string) string {
	mac := hmac.New(sha256.New, s.formIdleKey())
	for _, part := range []string{login, owner, form, strings.ToLower(proc)} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// resolveFormIdleTick проверяет тик и возвращает имя процедуры модуля формы.
func (s *Server) resolveFormIdleTick(login, owner string, form *metadata.FormModule, proc, token string) (string, error) {
	proc = strings.TrimSpace(proc)
	if form == nil || proc == "" || token == "" {
		return "", errFormIdleToken
	}
	want := s.formIdleToken(login, owner, form.Name, proc)
	if !hmac.Equal([]byte(want), []byte(strings.TrimSpace(token))) {
		return "", errFormIdleToken
	}
	decl := formModuleProc(form, proc)
	if decl == nil {
		return "", fmt.Errorf("процедура обработчика ожидания %q не найдена в модуле формы", proc)
	}
	return decl.Name.Literal, nil
}

func formModuleProc(form *metadata.FormModule, name string) *ast.ProcedureDecl {
	if form == nil {
		return nil
	}
	program, _ := form.ProgramAST.(*ast.Program)
	if program == nil {
		return nil
	}
	for _, p := range program.Procedures {
		if strings.EqualFold(p.Name.Literal, strings.TrimSpace(name)) {
			return p
		}
	}
	return nil
}

// newFormIdleBuiltins — ПодключитьОбработчикОжидания(ИмяПроцедуры,
// ИнтервалСек, Однократно = Ложь) и ОтключитьОбработчикОжидания(ИмяПроцедуры).
// Команды копятся в sink и уходят в ответ события; повторное подключение той
// же процедуры заменяет прежнее, как в 1С.
func (s *Server) newFormIdleBuiltins(sink *[]idleCommand, login, owner string, form *metadata.FormModule) map[string]any {
	put := func(cmd idleCommand) {
		for i := range *sink {
			if strings.EqualFold((*sink)[i].Proc, cmd.Proc) {
				(*sink)[i] = cmd
				return
			}
		}
		*sink = append(*sink, cmd)
	}
	attach := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("ПодключитьОбработчикОжидания(ИмяПроцедуры, ИнтервалСек[, Однократно]): ожидалось 2 или 3 аргумента, получено %d", len(args))
		}
		name := strings.TrimSpace(fmt.Sprint(args[0]))
		decl := formModuleProc(form, name)
		if decl == nil {
			return nil, fmt.Errorf("ПодключитьОбработчикОжидания: процедура %q не найдена в модуле формы", name)
		}
		interval, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(args[1])), 64)
		if err != nil {
			return nil, fmt.Errorf("ПодключитьОбработчикОжидания: интервал должен быть числом секунд, получено %v", args[1])
		}
		if interval < formIdleMinInterval {
			interval = formIdleMinInterval
		}
		once := false
		if len(args) == 3 {
			b, ok := args[2].(bool)
			if !ok && args[2] != nil {
				return nil, fmt.Errorf("ПодключитьОбработчикОжидания: Однократно должно быть Булево")
			}
			once = b
		}
		proc := decl.Name.Literal
		put(idleCommand{Proc: proc, Interval: interval, Once: once, Token: s.formIdleToken(login, owner, form.Name, proc)})
		return nil, nil
	})
	detach := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("ОтключитьОбработчикОжидания(ИмяПроцедуры): ожидался 1 аргумент, получено %d", len(args))
		}
		name := strings.TrimSpace(fmt.Sprint(args[0]))
		if decl := formModuleProc(form, name); decl != nil {
			name = decl.Name.Literal
		}
		put(idleCommand{Proc: name, Off: true})
		return nil, nil
	})
	return map[string]any{
		"ПодключитьОбработчикОжидания": attach,
		"AttachIdleHandler": attach,
		"ОтключитьОбработчикОжидания": detach,
		"DetachIdleHandler": detach,
	}
}

// formIdleState — поля сервера (ключ подписи создаётся при первом обращении).
type formIdleState struct {
	formIdleOnce   sync.Once
	formIdleSecret []byte
}

// formIdleLogin — пользователь запроса для подписи; на базе без пользователей
// пусто (все окна такой базы — один и тот же «пользователь»).
func formIdleLogin(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.Login
	}
	return ""
}

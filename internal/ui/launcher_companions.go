package ui

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/companions"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// launcherCompanionsFunc — экспортная функция общего модуля конфигурации,
// которая называет сопутствующие программы рабочего места для текущего
// пользователя: возвращает Массив ключей (или строку ключей через запятую).
// Например, колл-центр отвечает «callista» сотрудникам, у которых заполнен
// идентификатор в телефонии. Нет функции — программ нет.
const launcherCompanionsFunc = "СопутствующиеПрограммыПользователя"

// launcherCompanions — GET /ui/launcher/companions: ключи сопутствующих
// программ пользователя для окна Предприятия лаунчера. Сервер называет
// только ключи: путь и аргументы программы задаёт запись базы на рабочем
// месте, иначе конфигурация запускала бы произвольные программы на станциях.
// Функция исполняется только на чтение — ответ на вопрос «что запустить» не
// должен менять данные. Ошибка функции — пустой список: база открывается в
// любом случае.
func (s *Server) launcherCompanions(w http.ResponseWriter, r *http.Request) {
	keys := []string{}
	if auth.UserFromContext(r.Context()) != nil {
		var err error
		keys, err = s.companionKeys(r)
		if err != nil {
			slog.Warn("сопутствующие программы: функция конфигурации завершилась ошибкой",
				"function", launcherCompanionsFunc, "err", err)
			keys = []string{}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	respondJSONTo(w, map[string]any{"keys": keys})
}

func (s *Server) companionKeys(r *http.Request) ([]string, error) {
	if s.reg == nil || s.interp == nil {
		return []string{}, nil
	}
	decl := s.reg.GetModuleProc(launcherCompanionsFunc)
	if decl == nil {
		return []string{}, nil
	}
	ctx := storage.ReadOnlyContext(r.Context())
	vars, txState := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("launcher", uuid.Nil))
	defer rollbackDSLExecution(txState)
	var result any
	runErr := s.interp.RunWithResult(decl, &interpreter.MapThis{M: map[string]any{}}, &result, vars)
	if runErr = finishDSLExecution(txState, runErr); runErr != nil {
		return nil, runErr
	}
	var raw []string
	switch v := result.(type) {
	case nil:
	case *interpreter.Array:
		for _, item := range v.Iterate() {
			raw = append(raw, fmt.Sprint(item))
		}
	case string:
		raw = strings.Split(v, ",")
	default:
		return nil, fmt.Errorf("функция %s вернула %T, ожидался Массив ключей или строка", launcherCompanionsFunc, result)
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, k := range raw {
		key, ok := companions.NormalizeKey(k)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys, nil
}

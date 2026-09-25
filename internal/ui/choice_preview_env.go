package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Позитивное allowlist-окружение для choice_preview_proc (план 168, инварианты
// 8–9). Здесь СОЗДАЁТСЯ только перечисленное ниже — полного
// buildDSLVarsTx/buildDSLVarsWithMessagesTx в preview быть не может: на этом
// пути исполняется автоматически вызываемый код конфигурации, и любая
// write/external capability была бы доступна без ведома пользователя.
//
// Разрешено (инвариант 8): guarded Запрос, чтение разрешённых реквизитов
// переданных ссылок (Ссылки несут уже прошёдшие RLS+маску значения),
// Перечисления, ПредопределённыеЗначения, get-only Константы, текущий
// пользователь. Всё остальное — Движения, Справочники/Документы/Регистры,
// HTTP/Email/файлы/ИИ/оборудование/уведомления/команды ОС, фоновой задания,
// блокировки, аудит, планы обмена, нумераторы, транзакционные функции — в
// окружение не входит, обращение к ним даёт «переменная не определена» и 422.
//
// Вторым слоем остаётся серверная транзакция: вызов целиком идёт внутри
// host-owned транзакции, которая ОТКАТЫВАЕТСЯ ВСЕГДА, независимо от результата
// (см. runChoicePreviewProc).

// choiceGetOnly — get-only карта окружения. Присваивание
// `Константы.X = Значение` проваливается fail-closed, в том числе из вложенной
// функции общего модуля: сеттера у типа нет, аGet возвращает значение.
type choiceGetOnly struct {
	m map[string]any
}

func (c *choiceGetOnly) Get(name string) any { return c.m[name] }

// DynamicFieldAccessor — явный opt-in на доступ obj.Имя (интерпретер требует
// его отдельно от This). Запись через SetDynamicField fail-closed.
func (c *choiceGetOnly) GetDynamicField(name string) (any, bool) {
	v, ok := c.m[name]
	return v, ok
}
func (c *choiceGetOnly) SetDynamicField(name string, value any) bool {
	interpreter.RaiseUserError("запись недоступна в области просмотра выбора: " + name)
	return false
}
func (c *choiceGetOnly) Set(name string, v any) {
	interpreter.RaiseUserError("запись недоступна в области просмотра выбора: " + name)
}
func (c *choiceGetOnly) Keys() []string {
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	return keys
}

// CallMethod поддерживает протокол чтения Соответствия/структуры
// (Получить/Get); мутирующие методы (Вставить/Удалить/Очистить) fail-closed.
func (c *choiceGetOnly) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "получить", "get":
		if len(args) == 0 {
			return nil
		}
		key, ok := args[0].(string)
		if !ok {
			return nil
		}
		return c.m[key]
	case "вставить", "insert", "удалить", "delete", "очистить", "clear":
		interpreter.RaiseUserError("изменение недоступно в области просмотра выбора: " + method)
		return nil
	default:
		interpreter.RaiseUserError(fmt.Sprintf("метод %s недоступен в области просмотра выбора", method))
		return nil
	}
}

// buildChoicePreviewVars собирает allowlist-окружение. ctx ОБЯЗАН нести
// host-owned транзакцию: Запрос видит согласованный снимок, а сама транзакция
// всё равно откатывается вызывающим.
func (s *Server) buildChoicePreviewVars(ctx context.Context, rows []map[string]any, ent *metadata.Entity, previewField string, ctxVals map[string]any) map[string]any {
	vars := map[string]any{
		"__factory_Запрос":         interpreter.NewQueryFactory(ctx, s.store, s.reg),
		"__factory_Query":          interpreter.NewQueryFactory(ctx, s.store, s.reg),
		"Перечисления":             commonEnums(s.reg),
		"ПредопределённыеЗначения": interpreter.NewPredefinedRoot(ctx, s.store),
		"PredefinedValues":         interpreter.NewPredefinedRoot(ctx, s.store),
	}
	// Константы — только чтение: значения снимком на момент вызова. Переменная
	// есть ВСЕГДА (пустая, если констант нет): иначе присваивание молча не
	// доходило бы до get-only отказа.
	consts := map[string]any{}
	if vals, err := s.store.ListConstants(ctx); err == nil {
		consts = vals
	}
	vars["Константы"] = &choiceGetOnly{m: consts}
	vars["Constants"] = vars["Константы"]
	// Ссылки — элементы текущей видимой страницы: типизированная ссылка плюс
	// разрешённые (уже прошёдшие object read → row filter → field mask) реквизиты.
	// Новых чтений нет и быть не может: данные взяты из items.
	ids := &interpreter.Array{}
	for _, row := range rows {
		id := row["id"]
		member := map[string]any{}
		if raw, ok := id.(string); ok {
			if _, err := uuid.Parse(raw); err == nil {
				// Ссылка — uuid-строка: конфигурация сравнивает ключи результата
				// со Строка(Ссылка), а Запрос принимает её параметром.
				member["Ссылка"] = raw
				member["Ref"] = raw
			}
		}
		for k, v := range row {
			if k == "id" {
				continue
			}
			member[k] = v
		}
		// Платформенная Структура: полный доступ на чтение (Стр.Ссылка,
		// Стр.Наименование); правки функции оседают в копии и на ответ не влияют.
		ids.CallMethod("добавить", []any{interpreter.NewStructFromMap(member)})
	}
	vars["Ссылки"] = ids
	vars["Refs"] = ids
	// Контекст — только объявленные в choice_context параметры, восстановленные
	// и типизированные сервером (см. resolveChoicePreviewContext).
	// Платформенное Соответствие поверх КОПИИ значений: правки функции оседают
	// в копии. Константы остаются get-only (choiceGetOnly): мембрана рантайма
	// отклоняет запись с внятным отказом.
	ctxMapObj := &interpreter.Map{}
	for k, v := range ctxVals {
		ctxMapObj.CallMethod("вставить", []any{k, v})
	}
	vars["Контекст"] = ctxMapObj
	vars["Context"] = ctxMapObj
	// Текущий пользователь — только чтение профиля.
	if u := auth.UserFromContext(ctx); u != nil {
		vars["ТекущийПользователь"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
			return &choiceGetOnly{m: map[string]any{
				"ИД": u.ID, "Имя": u.Login, "ПолноеИмя": u.FullName, "Админ": u.IsAdmin,
			}}, nil
		})
		vars["CurrentUser"] = vars["ТекущийПользователь"]
		vars["ИмяПользователя"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
			return u.Login, nil
		})
		vars["UserName"] = vars["ИмяПользователя"]
	}
	return vars
}

// commonEnums — карта перечислений (значение = строка значения), как в общем
// окружении: чистое чтение метаданных.
func commonEnums(reg interface {
	Enums() []*metadata.Enum
}) *interpreter.MapThis {
	enumsMap := make(map[string]any)
	for _, e := range reg.Enums() {
		inner := make(map[string]any, len(e.Values))
		for _, v := range e.Values {
			inner[v] = v
		}
		enumsMap[e.Name] = &interpreter.MapThis{M: inner}
	}
	return &interpreter.MapThis{M: enumsMap}
}

// resolveChoicePreviewProc находит СТРОГО квалифицированную экспортную функцию
// нужной арности: `Модуль.Функция`, ровно два параметра, Экспорт. Короткое имя
// и неэкспортная функция — отказ (блокер 4 круга 2).
func resolveChoicePreviewProc(reg interface {
	GetModuleNamespacedProc(moduleName, procName string) *ast.ProcedureDecl
}, name string) (*ast.ProcedureDecl, error) {
	mod, fn, ok := strings.Cut(strings.TrimSpace(name), ".")
	if !ok || strings.TrimSpace(mod) == "" || strings.TrimSpace(fn) == "" || strings.Contains(fn, ".") {
		return nil, fmt.Errorf("choice_preview_proc должен быть вида Модуль.Функция, получено %q", name)
	}
	proc := reg.GetModuleNamespacedProc(mod, fn)
	if proc == nil {
		return nil, fmt.Errorf("функция %s не найдена в модуле %s", fn, mod)
	}
	if !proc.Export {
		return nil, fmt.Errorf("функция %s.%s должна быть Экспорт", mod, fn)
	}
	if len(proc.Params) != 2 {
		return nil, fmt.Errorf("функция %s.%s должна принимать ровно два параметра (Ссылки, Контекст), сейчас %d", mod, fn, len(proc.Params))
	}
	return proc, nil
}

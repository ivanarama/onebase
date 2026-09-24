package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// ОткрытьФорму (#1557, план 158 срез B): navigation-only переход к уже
// записанному объекту из обработчика формы. Ссылка обязана быть типизированной
// (ДокументСсылка.X / СправочникСсылка.X); канонический URL строит сервер,
// от браузера не приходит ничего. Переход доставляется только той вкладке,
// что отправила form-event (поле navigation в ответе), а не через SSE.

// navigationPayload уходит клиенту в ответе form-event.
type navigationPayload struct {
	URL string `json:"url"`
}

// newNavigationBuiltin строит билтин ОткрытьФорму(Ссылка). Перед постановкой
// payload выполняется recheck прав: чтение цели + RLS-предикат; недоступная и
// несуществующая цели неотличимы и дают ошибку обработчику.
func newNavigationBuiltin(sink **navigationPayload, reg *runtime.Registry, store *storage.DB, user *auth.User) interpreter.BuiltinFunc {
	return interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("ОткрытьФорму(Ссылка): ожидалась типизированная ссылка")
		}
		ref, ok := args[0].(*interpreter.Ref)
		if !ok || ref == nil || ref.UUID == "" || ref.Type == "" || ref.Kind == "" {
			return nil, fmt.Errorf("ОткрытьФорму: аргумент должен быть типизированной ссылкой (ДокументСсылка.X / СправочникСсылка.X)")
		}
		if _, err := uuid.Parse(ref.UUID); err != nil {
			return nil, fmt.Errorf("ОткрытьФорму: ссылка не несёт корректного идентификатора")
		}
		entity := reg.GetEntity(ref.Type)
		if entity == nil {
			return nil, fmt.Errorf("ОткрытьФорму: сущность %q не найдена", ref.Type)
		}
		checker := widget.New(reg, store)
		checker.User = user
		if !checker.ReferenceAllowed(context.Background(), entity.Name, ref.UUID) {
			return nil, fmt.Errorf("ОткрытьФорму: запись недоступна")
		}
		*sink = &navigationPayload{
			URL: "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + ref.UUID,
		}
		return nil, nil
	})
}

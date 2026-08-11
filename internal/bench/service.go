package bench

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// entityserviceFacade прячет сборку DSL-vars и форму SaveRequest, чтобы цикл
// измерения в bench.go оставался про латентность, а не про DTO entityservice.
type entityserviceFacade struct {
	svc *entityservice.Service
}

func newEntityserviceFacade(db *storage.DB, reg *runtime.Registry, interp *interpreter.Interpreter) *entityserviceFacade {
	svc := &entityservice.Service{
		Store:  db,
		Reg:    reg,
		Interp: interp,
		// Тот же набор DSL-переменных, что даёт REST-слой (Перечисления,
		// Константы, Запрос, Движения) — этого достаточно эталонному OnPost.
		BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
			return dslvars.Common{Ctx: c, Reg: reg, Store: db, Movements: mc}.Build(), nil
		},
	}
	return &entityserviceFacade{svc: svc}
}

// postDocument создаёт и проводит документ. DSL-ошибку хука превращает в
// обычную error — для бенча это сбой сценария, а не «бизнес-исключение».
func (f *entityserviceFacade) postDocument(ctx context.Context, doc *metadata.Entity, id uuid.UUID, fields map[string]any) error {
	res, err := f.svc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     id,
		IsNew:  true,
		Fields: fields,
		Action: "post",
	})
	if err != nil {
		return err
	}
	if res.DSLError != "" {
		return fmt.Errorf("OnPost DSL error: %s", res.DSLError)
	}
	return nil
}

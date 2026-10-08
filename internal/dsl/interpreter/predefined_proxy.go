package interpreter

import (
	"context"

	"github.com/ivantit66/onebase/internal/storage"
)

// PredefinedDB is the minimal storage interface for predefined item lookup.
// Returns the UUID of a predefined item as a string.
type PredefinedDB interface {
	GetPredefinedIDStr(ctx context.Context, entityName, itemName string) (string, error)
}

// PredefinedRoot is the DSL global ПредопределённыеЗначения / PredefinedValues.
// Each property access (.Валюта) returns a PredefinedCatalogProxy for that entity.
type PredefinedRoot struct {
	db  PredefinedDB
	ctx context.Context
}

// NewPredefinedRoot creates the root object for injection as DSL extraVar.
func NewPredefinedRoot(ctx context.Context, db PredefinedDB) *PredefinedRoot {
	return &PredefinedRoot{db: db, ctx: ctx}
}

func (r *PredefinedRoot) Get(entityName string) any {
	return &PredefinedCatalogProxy{entityName: entityName, db: r.db, ctx: r.ctx}
}

func (r *PredefinedRoot) Set(_ string, _ any) {}

// PredefinedCatalogProxy resolves individual predefined items by name.
// ПредопределённыеЗначения.Валюта.Рубль → UUID string
type PredefinedCatalogProxy struct {
	entityName string
	db         PredefinedDB
	ctx        context.Context
}

func (p *PredefinedCatalogProxy) Get(itemName string) any {
	id, err := p.db.GetPredefinedIDStr(p.ctx, p.entityName, itemName)
	if err != nil {
		if storage.IsNotFound(err) {
			panic(userError{
				Msg: "Предопределённый элемент " + p.entityName + "." + itemName + " не найден",
				Err: err,
			})
		}
		panic(userError{
			Msg: "Не удалось получить предопределённый элемент " + p.entityName + "." + itemName + ": " + err.Error(),
			Err: err,
		})
	}
	return id
}

func (p *PredefinedCatalogProxy) Set(_ string, _ any) {}

package interpreter

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/typedempty"
)

// Ссылочная константа не должна выдавать UUID за наименование (#1536):
// подпись даёт хост-презентер, без него подпись пустая, UUID сохраняется.
func TestConstantsRootRefPresentation(t *testing.T) {
	desc, _ := typedempty.FromConstant(&metadata.Constant{
		Name: "ОсновнойСклад", Type: metadata.FieldType("reference:Склады"), RefEntity: "Склады",
	})
	declared := []DeclaredConstant{{Name: "ОсновнойСклад", Descriptor: desc}}
	values := map[string]any{"ОсновнойСклад": "2c4be4b8-a41f-44ec-9170-b91afbe0b048"}
	factory := func(d typedempty.Descriptor) any { return &Ref{Type: d.RefEntity} }

	ref, ok := NewTypedConstantsRoot(context.Background(), nil, declared, values, factory).Get("ОсновнойСклад").(*Ref)
	if !ok {
		t.Fatalf("константа вернула %T, ожидалась ссылка", ref)
	}
	if ref.UUID != "2c4be4b8-a41f-44ec-9170-b91afbe0b048" {
		t.Fatalf("UUID = %q, ожидали сохранённый идентификатор", ref.UUID)
	}
	if ref.Name != "" {
		t.Fatalf("без презентера подпись = %q, ожидали пустую: UUID подписью не служит", ref.Name)
	}

	live := context.WithValue(context.Background(), ctxKey{}, "live")
	var seenCtx context.Context
	var seenEntity, seenUUID string
	presented := NewTypedConstantsRoot(context.Background(), nil, declared, values, factory).
		WithRefPresenter(func(ctx context.Context, entityName, uuid string) string {
			seenCtx, seenEntity, seenUUID = ctx, entityName, uuid
			return "Главный склад"
		}).
		WithRefCtxSource(NewStaticCtx(live))
	ref, ok = presented.Get("ОсновнойСклад").(*Ref)
	if !ok {
		t.Fatalf("с презентером константа вернула %T, ожидалась ссылка", ref)
	}
	if ref.Name != "Главный склад" {
		t.Fatalf("подпись = %q, ожидали %q", ref.Name, "Главный склад")
	}
	if seenEntity != "Склады" || seenUUID != "2c4be4b8-a41f-44ec-9170-b91afbe0b048" {
		t.Fatalf("презентеру пришло entity=%q uuid=%q", seenEntity, seenUUID)
	}
	if seenCtx != live {
		t.Fatal("презентер получил статический контекст вместо живого источника")
	}
	// Пустой ответ презентера — недоступная цель: подпись остаётся пустой.
	empty := NewTypedConstantsRoot(context.Background(), nil, declared, values, factory).
		WithRefPresenter(func(context.Context, string, string) string { return "" })
	ref, _ = empty.Get("ОсновнойСклад").(*Ref)
	if ref.Name != "" || ref.UUID == "" {
		t.Fatalf("недоступная цель: подпись = %q, UUID = %q", ref.Name, ref.UUID)
	}
}

type ctxKey struct{}

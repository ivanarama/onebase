package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// ОткрытьФорму (#1557) был зарегистрирован только в формах сущностей. Форма
// обработки — самое частое место для «нашли документ — открыли его», а check
// вызов пропускал (словарь билтинов общий): рантайм отвечал unknown function.
// Тот же класс, что #1683 для ПоказатьВопрос. Проверка — через публичный
// form-event обработки.
func TestOpenForm_ProcessorForm(t *testing.T) {
	направления := &metadata.Entity{
		Name: "Направления", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Открыть",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ОткрытьНажатие"},
	})
	form.ProgramAST = mustParse(t, `
Процедура ОткрытьНажатие()
	Ссылка = Справочники.Направления.НайтиПоНаименованию("Север");
	ОткрытьФорму(Ссылка);
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "ПоискНаправления", Forms: []*metadata.FormModule{form}}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{направления}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, направления.Name, id, map[string]any{"Наименование": "Север"}, направления); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{направления}})
	registry.LoadProcessors([]*processor.Processor{proc})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	srv := &Server{
		store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter(),
	}
	srv.entitySvc = srv.newEntityService(nil)

	body := processorClickBody("Открыть")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	want := "/ui/catalog/Направления/" + id.String()
	if resp.Navigation == nil || resp.Navigation.URL != want {
		t.Fatalf("navigation = %+v, ожидался переход на %s", resp.Navigation, want)
	}
}

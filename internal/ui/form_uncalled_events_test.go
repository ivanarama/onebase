package ui

// Сторож перечня невызываемых событий формы (#1153). `onebase check` теперь
// говорит конфигуратору «это событие движок не вызывает», а перечень таких
// событий вычисляется в metadata как «известные минус диспетчеризуемые». Утверждение
// проверяется здесь, у самих диспетчеров: браузерный маршрут обязан отклонять
// каждое невызываемое событие, а каждое объявленное серверным — реально
// исполняться. Разъедется — предупреждение начнёт врать в одну из двух сторон:
// либо смолчит о мёртвом обработчике, либо оговорит рабочий.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Ни одно невызываемое событие не проходит через браузерную точку входа — ни на
// уровне формы, ни на элементе любого вида. Иначе `check` ругался бы на
// обработчик, который на самом деле работает.
func TestUncalledFormEvents_НеДостижимыИзБраузера(t *testing.T) {
	uncalled := metadata.UncalledFormEvents()
	if len(uncalled) == 0 {
		t.Fatal("перечень невызываемых пуст — сторож сверяет пустоту")
	}
	for _, event := range uncalled {
		handlers := map[metadata.FormEventType]string{event: "Обработчик"}

		var elements []*metadata.FormElement
		for _, kind := range metadata.KnownFormElementTypes() {
			elements = append(elements, &metadata.FormElement{
				Kind: kind, Name: "Элемент" + string(kind), Handlers: handlers,
			})
		}
		form := &metadata.FormModule{
			Name:       "ФормаОбъекта",
			LayoutKind: metadata.FormLayoutManaged,
			Handlers:   handlers,
			Elements:   elements,
		}

		if _, _, _, err := resolveBrowserFormEvent(form, "", string(event), false); err == nil {
			t.Errorf("событие %q принято на уровне формы, а числится невызываемым", event)
		}
		for _, el := range elements {
			if _, _, _, err := resolveBrowserFormEvent(form, el.Name, string(event), false); err == nil {
				t.Errorf("событие %q принято на элементе вида %q, а числится невызываемым", event, el.Kind)
			}
		}
	}
}

// Каждое событие из metadata.ServerFormEvents обязано иметь работающий
// серверный путь. Это единственный перечень, где «диспетчер есть» записано
// словами, а не выведено из таблицы, — новая строка в нём без пути запуска
// уронит тест на default, а не тихо погасит предупреждение проверки.
func TestServerFormEvents_ЗапускаютсяСервером(t *testing.T) {
	events := metadata.ServerFormEvents()
	if len(events) == 0 {
		t.Fatal("перечень серверных событий пуст")
	}
	for _, event := range events {
		t.Run(string(event), func(t *testing.T) {
			// Обработчик заявляет о себе исключением: сработал — отказ виден,
			// не сработал — тишина, которую и ловим.
			srv, ent := setupManagedEventsServer(t, `
Процедура ОбработчикСобытия()
	ВызватьИсключение("СОБЫТИЕ-СРАБОТАЛО");
КонецПроцедуры
`, map[metadata.FormEventType]string{event: "ОбработчикСобытия"},
				[]*metadata.FormElement{
					{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
				})

			ctx := context.Background()
			switch event {
			case metadata.FormEventOnReadAtServer:
				id := insertContragent(t, srv, ent, "КОНТРАГЕНТ")
				if rec := executeFormEditGET(t, srv, ent, id); rec.Code != http.StatusForbidden {
					t.Fatalf("ПриЧтенииНаСервере не исполнился: код ответа %d вместо 403", rec.Code)
				}
			case metadata.FormEventBeforeWrite, metadata.FormEventOnWrite:
				obj := &runtime.Object{
					ID: uuid.New(), Type: ent.Name, Kind: ent.Kind,
					Fields:        map[string]any{"Наименование": "СТАРОЕ"},
					TablePartRows: map[string][]map[string]any{},
				}
				var msgs []string
				err := srv.runPreSaveFormHooks(ctx, ent, obj, &msgs)
				if err == nil {
					t.Fatalf("%s не исполнился: запись не отменена", event)
				}
				if !strings.Contains(err.Error(), "СОБЫТИЕ-СРАБОТАЛО") {
					t.Fatalf("%s: отказ пришёл не от обработчика: %v", event, err)
				}
			case metadata.FormEventAfterWrite:
				id := insertContragent(t, srv, ent, "КОНТРАГЕНТ")
				var msgs []string
				srv.runAfterWriteFormHook(ctx, ent, id, &msgs)
				if !strings.Contains(strings.Join(msgs, "\n"), "СОБЫТИЕ-СРАБОТАЛО") {
					t.Fatalf("ПослеЗаписи не исполнился: сообщений нет (%v)", msgs)
				}
			default:
				t.Fatalf("событие %q объявлено серверным, но у сторожа нет пути его запуска:"+
					" добавьте путь сюда или уберите событие из metadata.ServerFormEvents", event)
			}
		})
	}
}

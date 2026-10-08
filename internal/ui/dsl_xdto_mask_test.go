package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// СериализаторXDTO.ЗаписатьXML — такой же путь чтения, как форма, печать и
// списки: XDTOObject() отдаёт сериализатору сырой объект, минуя маскирование в
// Get(), поэтому без полевой политики выгрузка в XML была бы обходом маски в
// одну строку — Сообщить(СериализаторXDTO.ЗаписатьXML(Об)).

type xdtoMaskRegistry struct{ entities []*metadata.Entity }

func (r xdtoMaskRegistry) GetEntity(name string) *metadata.Entity {
	for _, e := range r.entities {
		if e != nil && strings.EqualFold(e.Name, name) {
			return e
		}
	}
	return nil
}

// loadMaskedClient кладёт в базу запись и отдаёт обёртку объекта, прочитанную
// под пользователем с заданной полевой политикой.
func loadMaskedClient(t *testing.T, fields auth.FieldPolicies) (*catWriter, *interpreter.XDTOSerializer) {
	t.Helper()
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	uctx := auth.ContextWithUser(ctx, uiMaskUser([]string{"read", "write"}, fields))
	obj, err := s.catObjectFactory(ctxSource{uctx}).LoadCatalogObject(client, id.String())
	if err != nil {
		t.Fatal(err)
	}
	w, ok := obj.(*catWriter)
	if !ok {
		t.Fatalf("LoadCatalogObject вернул %T, ожидалась обёртка объекта справочника", obj)
	}
	return w, interpreter.NewXDTOSerializer(xdtoMaskRegistry{entities: []*metadata.Entity{client, order}})
}

func writeXML(t *testing.T, s *interpreter.XDTOSerializer, obj any) string {
	t.Helper()
	out, ok := s.CallMethod("ЗаписатьXML", []any{obj}).(string)
	if !ok {
		t.Fatal("ЗаписатьXML не вернул строку")
	}
	return out
}

// Защищённый реквизит выгружается ровно так, как его видит текущий пользователь
// в форме: маска, а не реальное значение.
func TestDSL_XDTOWriteMasksProtectedField(t *testing.T) {
	w, serializer := loadMaskedClient(t, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})

	out := writeXML(t, serializer, w)
	if !strings.Contains(out, "<Телефон>••••••••4455</Телефон>") {
		t.Errorf("защищённый реквизит выгружен без маски:\n%s", out)
	}
	if strings.Contains(out, "+79161234455") {
		t.Errorf("в XML попало реальное значение защищённого реквизита:\n%s", out)
	}
	// Наименование — стандартный реквизит, он выводится под английским именем.
	if !strings.Contains(out, "<Description>Иванов</Description>") {
		t.Errorf("незащищённый реквизит изменён:\n%s", out)
	}
}

// Полностью недоступный реквизит выгружается пустым — как пустое поле в форме.
// Элемент при этом остаётся на месте: состав XML не зависит от прав, иначе
// приёмник получал бы разный набор элементов в зависимости от того, под кем
// выгружали.
func TestDSL_XDTOWriteEmptiesHiddenField(t *testing.T) {
	w, serializer := loadMaskedClient(t, auth.FieldPolicies{"Телефон": {Read: "hide"}})

	out := writeXML(t, serializer, w)
	if !strings.Contains(out, "<Телефон/>") {
		t.Errorf("недоступный реквизит не выгружен пустым элементом:\n%s", out)
	}
	if strings.Contains(out, "+79161234455") {
		t.Errorf("в XML попало значение недоступного реквизита:\n%s", out)
	}
}

// Без полевой политики вывод прежний — маскирование не меняет формат.
func TestDSL_XDTOWriteUnmaskedUnchanged(t *testing.T) {
	w, serializer := loadMaskedClient(t, nil)

	out := writeXML(t, serializer, w)
	if !strings.Contains(out, "<Телефон>+79161234455</Телефон>") {
		t.Errorf("без масок реквизит обязан выгружаться как есть:\n%s", out)
	}
}

// Значение, присвоенное самим модулем, не маскируется: оно принадлежит текущей
// операции, а не чужой записи. Тот же контракт, что у Get() обёртки.
func TestDSL_XDTOWriteKeepsValueAssignedByModule(t *testing.T) {
	w, serializer := loadMaskedClient(t, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})

	w.Set("Телефон", "+79990001122")
	out := writeXML(t, serializer, w)
	if !strings.Contains(out, "<Телефон>+79990001122</Телефон>") {
		t.Errorf("присвоенное модулем значение маскировать нельзя:\n%s", out)
	}
}

// Страж: обёртка, отдающая сериализатору сырой объект, обязана отдавать и
// полевую политику. Тесты выше проверяют сегодняшние две обёртки, а этот —
// завтрашнюю третью: XDTOObject() без XDTOMaskField() молча вернёт ровно ту
// дыру, ради которой маскирование сюда и добавлено, и ни один тест выше про
// неё не узнает.
func TestXDTOObjectSourceAlwaysMasks(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sources, maskers := map[string]string{}, map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("разобрать %s: %v", f, err)
		}
		for _, decl := range af.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			recv := fd.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			id, ok := recv.(*ast.Ident)
			if !ok {
				continue
			}
			switch fd.Name.Name {
			case "XDTOObject":
				sources[id.Name] = f
			case "XDTOMaskField":
				maskers[id.Name] = true
			}
		}
	}
	if len(sources) == 0 {
		t.Fatal("в пакете не нашлось ни одной обёртки с XDTOObject — сканер смотрит не туда")
	}
	for typ, file := range sources {
		if !maskers[typ] {
			t.Errorf("%s (%s) отдаёт сериализатору сырой объект, но не реализует XDTOMaskField: "+
				"СериализаторXDTO.ЗаписатьXML станет обходом полевой маски", typ, file)
		}
	}
}

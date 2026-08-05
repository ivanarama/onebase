package launcher

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// listManagedFormsFromFS должен возвращать nil/nil для проектов без forms/.
func TestListManagedFormsFromFS_NoDir(t *testing.T) {
	dir := t.TempDir()
	b := &Base{Path: dir, ConfigSource: "file"}
	forms, err := listManagedFormsFromFS(b)
	if err != nil {
		t.Fatalf("ошибка для отсутствующего forms/: %v", err)
	}
	if forms != nil {
		t.Errorf("ожидался nil, получено %d форм", len(forms))
	}
}

// listManagedFormsFromFS обходит forms/<entity>/*.form.yaml и подгружает
// соседний .form.os если он есть.
func TestListManagedFormsFromFS_TwoForms(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "forms", "контрагент"), 0o755); err != nil {
		t.Fatal(err)
	}
	yamlBody := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагент
elements: []
`
	osBody := "// модуль формы"
	if err := os.WriteFile(filepath.Join(dir, "forms", "контрагент", "объекта.form.yaml"), []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "forms", "контрагент", "объекта.form.os"), []byte(osBody), 0o644); err != nil {
		t.Fatal(err)
	}

	listYAML := `schema: onebase.form/v1
form:
  name: ФормаСписка
  kind: list
  entity: Контрагент
`
	if err := os.WriteFile(filepath.Join(dir, "forms", "контрагент", "списка.form.yaml"), []byte(listYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &Base{Path: dir, ConfigSource: "file"}
	forms, err := listManagedFormsFromFS(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(forms) != 2 {
		t.Fatalf("ожидалось 2 формы, получили %d", len(forms))
	}

	// Найдём объектную форму, проверим наличие .form.os и kind.
	var obj *cfgManagedForm
	for i := range forms {
		if strings.EqualFold(forms[i].Name, "объекта") {
			obj = &forms[i]
			break
		}
	}
	if obj == nil {
		t.Fatal("форма «объекта» не найдена")
		return
	}
	if !obj.HasOS {
		t.Error("HasOS должен быть true (есть .form.os)")
	}
	if obj.Kind != "object" {
		t.Errorf("kind = %q, ожидался object", obj.Kind)
	}
	if !strings.Contains(obj.YAML, "ФормаОбъекта") {
		t.Errorf("YAML не содержит имя формы: %q", obj.YAML)
	}
}

func TestListManagedFormsFromDBReadsSavedForm(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "managed-forms.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	yamlBody := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: сайт_атс
elements:
  - kind: ПолеВвода
    name: ТолькоНужноеПоле
    data_path: Объект.Наименование
`
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "forms/сайт_атс/формаобъекта.form.yaml", Content: []byte(yamlBody)},
	}, configdb.VersionOptions{Message: "seed managed form"}); err != nil {
		t.Fatal(err)
	}

	base := &Base{ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	req := httptest.NewRequest("GET", "/configurator/forms", nil)
	t.Cleanup(CloseAuthPools)
	h := &handler{}

	forms, err := h.listManagedForms(req, base)
	if err != nil {
		t.Fatalf("listManagedForms: %v", err)
	}
	if len(forms) != 1 {
		t.Fatalf("managed forms = %d, want 1: %+v", len(forms), forms)
	}
	if forms[0].Entity != "сайт_атс" || forms[0].Name != "формаобъекта" ||
		!strings.Contains(forms[0].YAML, "ТолькоНужноеПоле") {
		t.Fatalf("saved managed form was replaced or misread: %+v", forms[0])
	}

	treeForms, err := h.listManagedFormsFromDBNoRequest(ctx, base)
	if err != nil {
		t.Fatalf("listManagedFormsFromDBNoRequest: %v", err)
	}
	if len(treeForms) != 1 || !strings.Contains(treeForms[0].YAML, "ТолькоНужноеПоле") {
		t.Fatalf("configurator tree did not load saved managed form: %+v", treeForms)
	}
}

func TestDeleteManagedFormDBKeepsSimilarName(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "forms.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "forms/заказ/main.form.yaml", Content: []byte("form:\n  name: main\n")},
		{Path: "forms/заказ/main.form.os", Content: []byte("Процедура X()\nКонецПроцедуры\n")},
		{Path: "forms/заказ/main/_resources/a.bin", Content: []byte("res")},
		{Path: "forms/заказ/main2.form.yaml", Content: []byte("form:\n  name: main2\n")},
	}, configdb.VersionOptions{Message: "seed"}); err != nil {
		t.Fatal(err)
	}

	base := &Base{ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	req := httptest.NewRequest("POST", "/forms/delete", nil)
	t.Cleanup(CloseAuthPools)
	if err := deleteManagedForm(req, base, "Заказ", "main"); err != nil {
		t.Fatalf("deleteManagedForm: %v", err)
	}

	for _, p := range []string{"forms/заказ/main.form.yaml", "forms/заказ/main.form.os", "forms/заказ/main/_resources/a.bin"} {
		if _, ok, err := repo.ReadFile(ctx, p); err != nil || ok {
			t.Fatalf("%s should be deleted: ok=%v err=%v", p, ok, err)
		}
	}
	if content, ok, err := repo.ReadFile(ctx, "forms/заказ/main2.form.yaml"); err != nil || !ok || !strings.Contains(string(content), "main2") {
		t.Fatalf("similar form main2 should stay: ok=%v err=%v content=%q", ok, err, content)
	}
}

func TestDeleteManagedFormFSKeepsSimilarName(t *testing.T) {
	dir := t.TempDir()
	formDir := filepath.Join(dir, "forms", "заказ")
	if err := os.MkdirAll(filepath.Join(formDir, "main", "_resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(formDir, "main.form.yaml"):              "form:\n  name: main\n",
		filepath.Join(formDir, "main.form.os"):                "Процедура X()\nКонецПроцедуры\n",
		filepath.Join(formDir, "main", "_resources", "a.bin"): "res",
		filepath.Join(formDir, "main2.form.yaml"):             "form:\n  name: main2\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	base := &Base{Path: dir, ConfigSource: "file"}
	req := httptest.NewRequest("POST", "/forms/delete", nil)
	if err := deleteManagedForm(req, base, "Заказ", "main"); err != nil {
		t.Fatalf("deleteManagedForm: %v", err)
	}

	for _, p := range []string{
		filepath.Join(formDir, "main.form.yaml"),
		filepath.Join(formDir, "main.form.os"),
		filepath.Join(formDir, "main", "_resources", "a.bin"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be deleted, stat err=%v", p, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(formDir, "main2.form.yaml")); err != nil || !strings.Contains(string(content), "main2") {
		t.Fatalf("similar form main2 should stay: err=%v content=%q", err, content)
	}
}

func TestDeleteManagedFormFSRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{
		filepath.Join(dir, "victim.form.yaml"): "keep yaml",
		filepath.Join(dir, "victim.form.os"):   "keep os",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "victim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "victim", "keep.txt"), []byte("keep dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	base := &Base{Path: dir, ConfigSource: "file"}
	req := httptest.NewRequest("POST", "/forms/delete", nil)
	if err := deleteManagedForm(req, base, "..", "victim"); err == nil {
		t.Fatalf("deleteManagedForm accepted path traversal")
	}

	for _, p := range []string{
		filepath.Join(dir, "victim.form.yaml"),
		filepath.Join(dir, "victim.form.os"),
		filepath.Join(dir, "victim", "keep.txt"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should remain after rejected traversal: %v", p, err)
		}
	}
}

// extractFormKindFromYAML — точечный тест маленького helper'а.
func TestExtractFormKindFromYAML(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"form:\n  kind: object\n", "object"},
		{"form:\n  kind: list\n", "list"},
		{"form:\n  name: X\n", ""},
		{"  kind: choice\n", "choice"},
	}
	for _, c := range cases {
		got := extractFormKindFromYAML(c.in)
		if got != c.want {
			t.Errorf("extractFormKindFromYAML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// renderManagedFormPreview должен вернуть HTML с заголовком managed-маркером
// и отрисовать переданные элементы.
func TestRenderManagedFormPreview(t *testing.T) {
	fm := &metadata.FormModule{
		EntityName: "Контрагент",
		Title:      map[string]string{"ru": "Карточка контрагента"},
		Elements: []*metadata.FormElement{
			{
				Kind:     metadata.FormElementGroupBox,
				Name:     "Реквизиты",
				TitleMap: map[string]string{"ru": "Реквизиты"},
				Children: []*metadata.FormElement{
					{
						Kind:     metadata.FormElementField,
						Name:     "ПолеНаименование",
						TitleMap: map[string]string{"ru": "Наименование"},
						DataPath: "Объект.Наименование",
						Required: true,
					},
					{
						Kind:     metadata.FormElementCheckbox,
						Name:     "ПолеАктивен",
						TitleMap: map[string]string{"ru": "Активен"},
						DataPath: "Объект.Активен",
					},
				},
			},
		},
	}
	html := renderManagedFormPreview(fm)
	must := []string{
		"Карточка контрагента",
		"◇ managed",
		"<legend>Реквизиты</legend>",
		"Наименование",
		"Активен",
		"<input type=\"checkbox\"",
		`class="req"`, // звёздочка для required-поля
	}
	for _, s := range must {
		if !strings.Contains(html, s) {
			t.Errorf("preview не содержит %q", s)
		}
	}
}

// Отдельная Страница (вне набора СтраницыФормы — её можно бросить на холст)
// рисуется в предпросмотре именованным блоком со своими детьми, а не сообщением
// «предпросмотр не реализован» (баг живого теста после batch B/C, #164).
func TestRenderManagedFormPreview_StandalonePage(t *testing.T) {
	fm := &metadata.FormModule{
		EntityName: "Заказ",
		Elements: []*metadata.FormElement{
			{
				Kind:     metadata.FormElementPage,
				Name:     "Товары",
				TitleMap: map[string]string{"ru": "Товары"},
				Children: []*metadata.FormElement{
					{
						Kind:     metadata.FormElementField,
						Name:     "ПолеКомментарий",
						TitleMap: map[string]string{"ru": "Комментарий"},
						DataPath: "Объект.Комментарий",
					},
				},
			},
		},
	}
	html := renderManagedFormPreview(fm)
	if strings.Contains(html, "предпросмотр не реализован") {
		t.Errorf("отдельная страница даёт «предпросмотр не реализован»:\n%s", html)
	}
	for _, s := range []string{"<legend>Товары</legend>", "Комментарий"} {
		if !strings.Contains(html, s) {
			t.Errorf("preview отдельной страницы не содержит %q:\n%s", s, html)
		}
	}
}

// Предпросмотр табличной части с выбранными колонками рисует реальную таблицу с
// заголовками колонок, а не строку-заглушку (обратная связь по живому тесту #164).
func TestRenderManagedFormPreview_TablePartColumns(t *testing.T) {
	fm := &metadata.FormModule{
		EntityName: "Заказ",
		Elements: []*metadata.FormElement{
			{
				Kind: metadata.FormElementTablePart, Name: "ТабТовары",
				TitleMap: map[string]string{"ru": "Товары"},
				DataPath: "Объект.Товары",
				Children: []*metadata.FormElement{
					{Kind: metadata.FormElementColumn, Name: "КолНоменклатура",
						TitleMap: map[string]string{"ru": "Номенклатура"}, DataPath: "Объект.Товары.Номенклатура"},
					{Kind: metadata.FormElementColumn, Name: "КолКоличество",
						TitleMap: map[string]string{"ru": "Количество"}, DataPath: "Объект.Товары.Количество"},
				},
			},
		},
	}
	html := renderManagedFormPreview(fm)
	if strings.Contains(html, "Предпросмотр упрощённый") {
		t.Errorf("табличная часть осталась строкой-заглушкой:\n%s", html)
	}
	for _, s := range []string{"tp-prev-tbl", "<th>Номенклатура</th>", "<th>Количество</th>"} {
		if !strings.Contains(html, s) {
			t.Errorf("в предпросмотре ТЧ нет %q:\n%s", s, html)
		}
	}
}

// Табличная часть без выбранных колонок — подсказка, не таблица и не заглушка.
func TestRenderManagedFormPreview_TablePartNoColumns(t *testing.T) {
	fm := &metadata.FormModule{
		EntityName: "Заказ",
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementTablePart, Name: "ТабТовары",
				TitleMap: map[string]string{"ru": "Товары"}, DataPath: "Объект.Товары"},
		},
	}
	html := renderManagedFormPreview(fm)
	if !strings.Contains(html, "Колонки не выбраны") {
		t.Errorf("нет подсказки про невыбранные колонки:\n%s", html)
	}
}

// previewErrorHTML экранирует сообщение и возвращает валидный HTML.
func TestPreviewErrorHTML(t *testing.T) {
	html := previewErrorHTML("parse yaml: line 5: bad <token>")
	if !strings.Contains(html, "Ошибка YAML") {
		t.Error("нет заголовка")
	}
	if !strings.Contains(html, "&lt;token&gt;") {
		t.Error("XML-токены не экранированы")
	}
}

// Регрессия (issue #31 в /tmp): при создании новой формы Monaco-editor
// получал значение через цепочку html.EscapeString + JS replace, что
// ломалось на кириллице и переносах строк — preview-handler ругался
// `did not find expected alphabetic or numeric character`.
// Исправлено через json.Marshal helper jsString — возвращает обрамлённый
// в кавычки JS-литерал, валидный в любых сценариях.
func TestFormsEditor_YAMLEmbeddedAsValidJSLiteral(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "test-base"},
		EditingForm: &cfgManagedForm{
			Entity: "Контрагент",
			Name:   "ФормаОбъекта",
			Kind:   "object",
			YAML: `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагент
  title:
    ru: "Карточка"
`,
			OS: "// @directive=&НаСервере\nПроцедура X()\nКонецПроцедуры\n",
		},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()

	// YAML должен быть встроен как корректный JS-литерал в одну строку
	// с экранированными переносами и сохранением кириллицы.
	if !strings.Contains(html, `"schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n`) {
		t.Errorf("YAML не встроен как валидный JS-литерал (кириллица или \\n не экранированы корректно).\n"+
			"Фрагмент HTML вокруг yamlEditor:\n%s", extractContext(html, "yamlEditor", 300))
	}

	// OS-модуль с символом & (директива) — критический случай.
	// json.Marshal по умолчанию SetEscapeHTML=true, превращает & в &
	// (это HTML-safe escape, валиден как JS-литерал — браузер декодирует обратно).
	// Прежний баг давал именно `&` в неэкранированном виде, ломая JS-парсинг.
	// json.Marshal с дефолтным SetEscapeHTML=true превращает & в &
	// (это валидный JS-литерал, браузер при парсинге восстановит &).
	// Принимаем оба варианта: и литеральный &, и &.
	hasLiteralAmp := strings.Contains(html, `"// @directive=&НаСервере\n`)
	hasUnicodeAmp := strings.Contains(html, "\"// @directive=\\u0026НаСервере\\n")
	if !hasLiteralAmp && !hasUnicodeAmp {
		t.Errorf("OS-модуль с &НаСервере не встроен корректно.\n"+
			"Фрагмент HTML вокруг osEditor:\n%s", extractContext(html, "osEditor", 300))
	}

	// Прежний баг: HTML-escape-цепочки .replace(/&lt;/g,'<') не должно быть.
	if strings.Contains(html, ".replace(/&lt;/g,'<')") {
		t.Error("в шаблоне всё ещё используется старая цепочка HTML-escape replace — JS-escape должен идти через jsString")
	}
}

// Регрессия (follow-up #164, слайс A): back-link «← В конфигуратор» терял
// объект-источник и вёл в корень. С ?from=<nodeID> он обязан вести на узел
// дерева, из которого открыли редактор; без from — фолбэк на e-<Entity>.
func TestFormsEditor_BackLinkCarriesFrom(t *testing.T) {
	base := &Base{ID: "b1"}
	form := &cfgManagedForm{Entity: "Customer", Name: "ФормаОбъекта", Kind: "object", YAML: "schema: onebase.form/v1\n"}

	// С from → back-link и hidden-поля сохраняют узел-источник.
	var buf bytes.Buffer
	withFrom := &configuratorData{Base: base, EditingForm: form, FormEditFrom: "proc-Загрузка"}
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", withFrom); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	gotFrom := buf.String()
	if !strings.Contains(gotFrom, "/bases/b1/configurator?tab=tree&select=proc-") {
		t.Errorf("back-link не ведёт на from-узел.\n%s", extractContext(gotFrom, "В конфигуратор", 220))
	}
	if !strings.Contains(gotFrom, `name="from" value="proc-Загрузка"`) {
		t.Error("в save/delete формах нет hidden-поля from")
	}

	// Без from → фолбэк на e-<Entity>.
	buf.Reset()
	noFrom := &configuratorData{Base: base, EditingForm: form}
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", noFrom); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	if !strings.Contains(buf.String(), "/bases/b1/configurator?tab=tree&select=e-Customer") {
		t.Errorf("фолбэк back-link не e-<Entity>.\n%s", extractContext(buf.String(), "В конфигуратор", 220))
	}
}

// extractContext возвращает кусок строки вокруг указанного маркера.
func extractContext(s, marker string, span int) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return "(маркер не найден)"
	}
	start := i - span
	if start < 0 {
		start = 0
	}
	end := i + span
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}

// formFiles генерирует пути в lowercase.
func TestFormFiles(t *testing.T) {
	b := &Base{Path: "C:/proj"}
	yp, op := formFiles(b, "Контрагент", "ФормаОбъекта")
	wantYP := filepath.Join("C:/proj", "forms", "контрагент", "формаобъекта.form.yaml")
	wantOP := filepath.Join("C:/proj", "forms", "контрагент", "формаобъекта.form.os")
	if yp != wantYP {
		t.Errorf("yp = %q, want %q", yp, wantYP)
	}
	if op != wantOP {
		t.Errorf("op = %q, want %q", op, wantOP)
	}
}

// Issue #133: заготовка «Форма объекта» строится из реальных реквизитов объекта.
// У обработки нет реквизита «Наименование» — шаблон не должен его подставлять.
func TestNewFormYAMLTemplate_ScaffoldByKind(t *testing.T) {
	proj := &project.Project{
		Entities: []*metadata.Entity{
			{Name: "Контрагент", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "ИНН", Title: "ИНН контрагента", Type: metadata.FieldTypeString},
			}},
		},
		Processors: []*processor.Processor{
			{Name: "ЗагрузкаЦен", Params: []processor.Param{{Name: "Файл", Label: "Файл с ценами"}}},
			{Name: "ПустаяОбработка"}, // без параметров — главный кейс issue #133
		},
	}

	t.Run("обработка без параметров — пустая группа, без Наименования", func(t *testing.T) {
		attrs := objectScaffoldAttrs(proj, "ПустаяОбработка")
		if len(attrs) != 0 {
			t.Fatalf("ожидалось 0 реквизитов, получили %d", len(attrs))
		}
		y := newFormYAMLTemplate("ПустаяОбработка", "ФормаОбъекта", "object", attrs)
		if strings.Contains(y, "Наименование") {
			t.Errorf("шаблон обработки не должен содержать «Наименование»:\n%s", y)
		}
		if !strings.Contains(y, "children: []") {
			t.Errorf("ожидалась пустая группа children: [] :\n%s", y)
		}
		// Сгенерированная форма обязана валидно загружаться.
		mustLoadForm(t, y, "ПустаяОбработка")
	})

	t.Run("обработка с параметрами — поля из параметров", func(t *testing.T) {
		attrs := objectScaffoldAttrs(proj, "ЗагрузкаЦен")
		y := newFormYAMLTemplate("ЗагрузкаЦен", "ФормаОбъекта", "object", attrs)
		if strings.Contains(y, "Наименование") {
			t.Errorf("шаблон обработки не должен содержать «Наименование»:\n%s", y)
		}
		if !strings.Contains(y, "data_path: Объект.Файл") {
			t.Errorf("ожидалось поле по параметру «Файл»:\n%s", y)
		}
		if !strings.Contains(y, `ru: "Файл с ценами"`) {
			t.Errorf("ожидалась подпись из Label параметра:\n%s", y)
		}
		mustLoadForm(t, y, "ЗагрузкаЦен")
	})

	t.Run("справочник — поля из метаданных, включая Наименование", func(t *testing.T) {
		attrs := objectScaffoldAttrs(proj, "Контрагент")
		y := newFormYAMLTemplate("Контрагент", "ФормаОбъекта", "object", attrs)
		if !strings.Contains(y, "data_path: Объект.Наименование") {
			t.Errorf("шаблон справочника должен содержать поле «Наименование»:\n%s", y)
		}
		if !strings.Contains(y, "data_path: Объект.ИНН") || !strings.Contains(y, `ru: "ИНН контрагента"`) {
			t.Errorf("шаблон справочника должен содержать поле «ИНН» с синонимом:\n%s", y)
		}
		mustLoadForm(t, y, "Контрагент")
	})

	t.Run("неизвестный объект — фолбэк на пустую группу", func(t *testing.T) {
		if attrs := objectScaffoldAttrs(proj, "НетТакого"); attrs != nil {
			t.Errorf("ожидался nil для неизвестного объекта, получили %v", attrs)
		}
		y := newFormYAMLTemplate("НетТакого", "ФормаОбъекта", "object", nil)
		if strings.Contains(y, "Наименование") {
			t.Errorf("фолбэк-шаблон не должен содержать «Наименование»:\n%s", y)
		}
	})
}

// mustLoadForm проверяет, что YAML-заготовка валидно парсится загрузчиком
// управляемых форм (тем же, что и live-валидация в конфигураторе).
func mustLoadForm(t *testing.T, yaml, entity string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.form.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.NewManagedFormLoader().LoadFormFile(p, entity); err != nil {
		t.Errorf("сгенерированная форма не загружается: %v\n%s", err, yaml)
	}
}

// Issue #134: в редакторе форм рендерится палитра реквизитов объекта —
// перетаскиваемые/кликабельные чипы, вставляющие поле ПолеВвода.
func TestFormsEditor_AttrPalette(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "test-base"},
		EditingForm: &cfgManagedForm{
			Entity: "Контрагент", Name: "ФормаОбъекта", Kind: "object",
			YAML: "schema: onebase.form/v1\n",
		},
		EditingFormAttrs: []formScaffoldAttr{
			{Name: "Наименование"},                  // Title пуст → подпись = Name
			{Name: "ИНН", Title: "ИНН контрагента"}, // Title задан
		},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`class="attr-palette"`,
		`data-attr="Наименование"`,
		`data-attr="ИНН"`,
		`data-title="ИНН контрагента"`,
		`onclick="insertFieldFromChip(this)"`,
		`draggable="true"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в редакторе формы нет %q", want)
		}
	}
	// У реквизита без Title подпись чипа = его имя (data-title=Name). data-type
	// пустой (в тесте тип не задан) — но присутствует между data-attr и data-title.
	if !strings.Contains(html, `data-attr="Наименование" data-type="" data-title="Наименование"`) {
		t.Errorf("ожидался data-title=Name для реквизита без синонима")
	}
}

// Палитра табличных частей рендерится из EditingFormTableParts и несёт data-tp
// для drag-drop вставки ТЧ (follow-up #164, слайс D1).
func TestFormsEditor_TablePartPalette(t *testing.T) {
	data := &configuratorData{
		Base:        &Base{ID: "b"},
		EditingForm: &cfgManagedForm{Entity: "Заказ", Name: "ФормаОбъекта", Kind: "object", YAML: "schema: onebase.form/v1\n"},
		EditingFormTableParts: []formTablePart{
			{Name: "Товары", Title: "Товары", Columns: []formScaffoldAttr{{Name: "Номенклатура"}}},
		},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `id="tablepart-palette"`) || !strings.Contains(out, `data-tp="Товары"`) {
		t.Errorf("нет палитры табличных частей:\n%s", extractContext(out, "tablepart", 220))
	}
}

// Редактор встраивает состав колонок ТЧ как JSON (_tablePartsList) — источник
// данных для редактора колонок D2 (follow-up #164).
func TestFormsEditor_EmbedsTablePartColumns(t *testing.T) {
	data := &configuratorData{
		Base:        &Base{ID: "b"},
		EditingForm: &cfgManagedForm{Entity: "Заказ", Name: "ФормаОбъекта", Kind: "object", YAML: "schema: onebase.form/v1\n"},
		EditingFormTableParts: []formTablePart{
			{Name: "Товары", Columns: []formScaffoldAttr{{Name: "Номенклатура", Title: "Ном."}}},
		},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "_tablePartsList =") {
		t.Error("нет встроенного списка табличных частей")
	}
	if !strings.Contains(out, `"name":"Товары"`) || !strings.Contains(out, `"name":"Номенклатура"`) {
		t.Errorf("в _tablePartsList нет состава колонок:\n%s", extractContext(out, "_tablePartsList", 220))
	}
}

// Без реквизитов (например, объект не найден) палитра не рендерится.
func TestFormsEditor_AttrPalette_EmptyHidden(t *testing.T) {
	data := &configuratorData{
		Base:        &Base{ID: "test-base"},
		EditingForm: &cfgManagedForm{Entity: "Стуб", Name: "ФормаОбъекта", Kind: "object", YAML: "schema: onebase.form/v1\n"},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	if strings.Contains(buf.String(), `class="attr-palette"`) {
		t.Error("палитра не должна рендериться без реквизитов")
	}
}

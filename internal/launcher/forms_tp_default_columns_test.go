package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/formdoc"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Расхождение #1123: рантайм (managedTPColumnPlan) трактует «ни одного ребёнка
// kind: Колонка» как «показать ВСЕ реквизиты», а конструктор изображал пустоту —
// холст писал «колонки не выбраны», галочки в панели стояли снятыми. Пользователь
// видел в работающей форме полную таблицу, а в конструкторе пустую ТЧ, и
// «исправлял» это галочкой, которая на самом деле убирает все колонки, кроме
// отмеченной. Так объявлены ТЧ во всех примерах конфигураций.

// tpFormYAML — форма из одной табличной части без явных колонок.
const tpFormYAML = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  title:
    ru: "Поступление"
elements:
  - kind: ТабличнаяЧасть
    name: ТабТовары
    title:
      ru: "Товары"
    data_path: Объект.Товары
`

func TestХолст_ТЧБезКолонокНеНазываетсяПустой(t *testing.T) {
	doc, err := formdoc.Load([]byte(tpFormYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	html, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if strings.Contains(html, "колонки не выбраны") {
		t.Errorf("холст называет пустой ТЧ, у которой рантайм покажет все колонки:\n%s", html)
	}
	if !strings.Contains(html, "все колонки (по умолчанию)") {
		t.Errorf("холст не называет состояние «показываются все колонки»:\n%s", html)
	}
}

// Явный состав холст обязан показывать как есть — иначе подпись «по умолчанию»
// появлялась бы там, где состав как раз задан руками.
func TestХолст_ТЧСКолонкамиПоказываетИх(t *testing.T) {
	src := strings.Replace(tpFormYAML, "    data_path: Объект.Товары\n",
		`    data_path: Объект.Товары
    children:
      - kind: Колонка
        name: КолЦена
        data_path: Объект.Товары.Цена
`, 1)
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	html, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if strings.Contains(html, "все колонки (по умолчанию)") {
		t.Errorf("холст назвал явный состав умолчанием:\n%s", html)
	}
	if !strings.Contains(html, "Цена") {
		t.Errorf("холст не показал объявленную колонку:\n%s", html)
	}
}

// Ребёнок не-Колонка не должен считаться заданным составом: рантайм переходит в
// режим «показать выбранное» только по детям kind: Колонка.
func TestХолст_ПостороннийРебёнокНеСчитаетсяСоставом(t *testing.T) {
	src := strings.Replace(tpFormYAML, "    data_path: Объект.Товары\n",
		`    data_path: Объект.Товары
    children:
      - kind: Кнопка
        name: КнопкаЗаполнить
`, 1)
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	html, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if !strings.Contains(html, "все колонки (по умолчанию)") {
		t.Errorf("кнопка внутри ТЧ засчитана за состав колонок:\n%s", html)
	}
}

// Рантайм сопоставляет колонку реквизиту по data_path, field ИЛИ имени
// (managedTPFieldIndexForColumn), а в модель холста ключ field не попадал вовсе —
// поэтому панель свойств не могла поставить галочку такой колонке.
func TestМодельХолста_КолонкаЧерезFieldПопадаетВМодель(t *testing.T) {
	src := strings.Replace(tpFormYAML, "    data_path: Объект.Товары\n",
		`    data_path: Объект.Товары
    children:
      - kind: Колонка
        name: КолЦена
        field: Цена
`, 1)
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	model, err := canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	info, ok := model["elements.0.children.0"]
	if !ok {
		t.Fatalf("колонки нет в модели: %+v", model)
	}
	if info.Field != "Цена" {
		t.Errorf("ключ field не попал в модель холста: %+v", info)
	}
}

// insertColumns — материализация состава одной командой. Серия из N запросов с
// клиента при обрыве на середине оставила бы ТЧ с ЯВНЫМ составом, урезанным до
// места обрыва, то есть тихо спрятала бы колонки, которых никто не снимал.
func TestEditOp_InsertColumnsМатериализуетСоставЦеликом(t *testing.T) {
	res, err := applyEditOp([]byte(tpFormYAML), editOpRequest{
		Op:     "insertColumns",
		Parent: "elements.0",
		Columns: `[{"name":"КолНоменклатура","title":"Номенклатура","data_path":"Объект.Товары.Номенклатура"},
		           {"name":"КолКоличество","title":"Количество","data_path":"Объект.Товары.Количество"},
		           {"name":"КолСумма","title":"Сумма","data_path":"Объект.Товары.Сумма"}]`,
	})
	if err != nil {
		t.Fatalf("applyEditOp: %v", err)
	}
	if res.SelectedID != "elements.0" {
		t.Errorf("выделение ушло с табличной части: %q", res.SelectedID)
	}

	// Проверяем не текст YAML, а то, что из него прочитает рантайм: колонки
	// должны стать детьми ТЧ в присланном порядке.
	fm := loadFormModule(t, res.YAML)
	if len(fm.Elements) != 1 {
		t.Fatalf("ожидался один элемент формы, получено %d", len(fm.Elements))
	}
	var got []string
	for _, c := range fm.Elements[0].Children {
		if c.Kind != metadata.FormElementColumn {
			t.Errorf("среди детей ТЧ появился %q", c.Kind)
			continue
		}
		got = append(got, c.DataPath)
	}
	want := []string{"Объект.Товары.Номенклатура", "Объект.Товары.Количество", "Объект.Товары.Сумма"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("состав колонок не тот:\nбыло  %v\nнадо  %v\nYAML:\n%s", got, want, res.YAML)
	}
}

func TestEditOp_InsertColumnsОтвергаетПустоеИНеразборное(t *testing.T) {
	for name, req := range map[string]editOpRequest{
		"без parent":       {Op: "insertColumns", Columns: `[{"name":"К"}]`},
		"пустой список":    {Op: "insertColumns", Parent: "elements.0", Columns: `[]`},
		"неразборный JSON": {Op: "insertColumns", Parent: "elements.0", Columns: `{`},
	} {
		if _, err := applyEditOp([]byte(tpFormYAML), req); err == nil {
			t.Errorf("%s: команда принята, хотя состав задать нечем", name)
		}
	}
}

// loadFormModule прогоняет YAML через тот же загрузчик, которым форму читает
// рантайм: тест обязан идти путём пользователя, а не разбирать YAML по-своему.
func loadFormModule(t *testing.T, yamlSrc string) *metadata.FormModule {
	t.Helper()
	path := filepath.Join(t.TempDir(), "объекта.form.yaml")
	if err := os.WriteFile(path, []byte(yamlSrc), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Поступление")
	if err != nil {
		t.Fatalf("LoadFormFile: %v\nYAML:\n%s", err, yamlSrc)
	}
	return fm
}

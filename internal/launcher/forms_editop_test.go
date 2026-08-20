package launcher

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// HTTP-уровень: маршрут /forms/edit-op доходит до handler'а, парсит форму,
// применяет команду и отдаёт JSON — закрывает интеграционный разрыв поверх
// юнит-тестов applyEditOp.
func TestConfiguratorFormsEditOp_HTTP(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	b := &Base{Path: t.TempDir(), ConfigSource: "file"}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add base: %v", err)
	}
	h := &handler{store: s}

	form := url.Values{}
	form.Set("op", "insert")
	form.Set("parent", "elements.0")
	form.Set("index", "0")
	form.Set("kind", "ПолеВвода")
	form.Set("name", "ПолеТест")
	form.Set("data_path", "Объект.Тест")
	form.Set("yaml", canvasSample)

	req := httptest.NewRequest("POST", "/bases/"+b.ID+"/configurator/forms/edit-op", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	h.configuratorFormsEditOp(rr, req)

	if rr.Code != 200 {
		t.Fatalf("статус %d: %s", rr.Code, rr.Body.String())
	}
	var resp editOpResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("разбор JSON: %v; тело=%s", err, rr.Body.String())
	}
	if !resp.OK {
		t.Fatalf("ok=false, errors=%v", resp.Errors)
	}
	if !strings.Contains(resp.YAML, "ПолеТест") {
		t.Errorf("в YAML нет нового поля:\n%s", resp.YAML)
	}
	if resp.SelectedID != "elements.0.children.0" {
		t.Errorf("selectedId = %q", resp.SelectedID)
	}
	if _, ok := resp.Model["elements.0.children.0"]; !ok {
		t.Errorf("в модели нет нового узла: %v", resp.Model)
	}
}

// setProp через эндпоинт: значение пишется в YAML, выделение остаётся на узле,
// холст пере-рендерен с тем же node-id.
func TestApplyEditOp_SetProp(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op:    "setProp",
		Node:  "elements.0.children.1",
		Key:   "data_path",
		Value: "Объект.Изменено",
	})
	if err != nil {
		t.Fatalf("applyEditOp: %v", err)
	}
	if !strings.Contains(res.YAML, "data_path: Объект.Изменено") {
		t.Errorf("YAML не обновлён:\n%s", res.YAML)
	}
	if res.SelectedID != "elements.0.children.1" {
		t.Errorf("selectedId = %q", res.SelectedID)
	}
	if !strings.Contains(res.CanvasHTML, `data-node-id="elements.0.children.1"`) {
		t.Errorf("холст не содержит выбранный узел")
	}
}

// Чекбокс-свойство приходит строкой "true" → в YAML булев скаляр, не строка.
func TestApplyEditOp_BoolProp(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op:    "setProp",
		Node:  "elements.0.children.1",
		Key:   "readonly",
		Value: "true",
	})
	if err != nil {
		t.Fatalf("applyEditOp: %v", err)
	}
	if !strings.Contains(res.YAML, "readonly: true") {
		t.Errorf("bool-проп не булев:\n%s", res.YAML)
	}
}

// insert через эндпоинт: новый элемент попадает в YAML с title.ru и data_path,
// становится выбранным, и виден на холсте.
func TestApplyEditOp_Insert(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op:       "insert",
		Parent:   "elements.0",
		Index:    2,
		Kind:     "ПолеВвода",
		Name:     "ПолеНовое",
		DataPath: "Объект.Новое",
		TitleRU:  "Новое поле",
	})
	if err != nil {
		t.Fatalf("applyEditOp: %v", err)
	}
	if res.SelectedID != "elements.0.children.2" {
		t.Errorf("selectedId = %q, ожидался elements.0.children.2", res.SelectedID)
	}
	for _, want := range []string{"ПолеНовое", "Объект.Новое", "Новое поле"} {
		if !strings.Contains(res.YAML, want) {
			t.Errorf("YAML без %q:\n%s", want, res.YAML)
		}
	}
	if !strings.Contains(res.CanvasHTML, `data-node-id="elements.0.children.2"`) {
		t.Errorf("холст без нового узла:\n%s", res.CanvasHTML)
	}
}

// delete через эндпоинт: узел вместе с поддеревом исчезает из YAML и модели,
// выделение сбрасывается (follow-up #164, слайс B1).
func TestApplyEditOp_Delete(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op:   "delete",
		Node: "elements.0.children.0",
	})
	if err != nil {
		t.Fatalf("applyEditOp delete: %v", err)
	}
	if res.SelectedID != "" {
		t.Errorf("после удаления выделение должно сброситься, got %q", res.SelectedID)
	}
	if strings.Contains(res.YAML, "Объект.Номер") || strings.Contains(res.YAML, "ПолеНомер") {
		t.Errorf("удалённый узел остался в YAML:\n%s", res.YAML)
	}
	// Удалённого data_path нет ни в одном узле модели (бывший сосед сдвинулся
	// на тот же node-id, но это уже другой элемент).
	for id, info := range res.Model {
		if info.DataPath == "Объект.Номер" {
			t.Errorf("узел %q в модели всё ещё несёт удалённый data_path", id)
		}
	}
}

// insert контейнера (группа) даёт явный пустой children в YAML и узел-container
// в модели — чтобы внутрь сразу можно было ронять поля (follow-up #164, слайс C).
func TestApplyEditOp_InsertContainer(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op: "insert", Parent: "", Index: 1, Kind: "ГруппаФормы", Name: "Группа2", TitleRU: "Реквизиты",
	})
	if err != nil {
		t.Fatalf("applyEditOp insert container: %v", err)
	}
	if !strings.Contains(res.YAML, "children: []") {
		t.Errorf("у новой группы нет явного пустого children:\n%s", res.YAML)
	}
	info, ok := res.Model[res.SelectedID]
	if !ok || !info.Container {
		t.Errorf("новый узел не container: id=%q info=%+v", res.SelectedID, info)
	}
}

// insert табличной части: в YAML kind ТабличнаяЧасть с data_path и пустым
// children (контейнер колонок), узел становится выбранным container (D1).
func TestApplyEditOp_InsertTablePart(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op: "insert", Parent: "", Index: 1, Kind: "ТабличнаяЧасть",
		Name: "ТабСтроки", DataPath: "Объект.Строки", TitleRU: "Строки",
	})
	if err != nil {
		t.Fatalf("applyEditOp insert tablepart: %v", err)
	}
	for _, want := range []string{"kind: ТабличнаяЧасть", "data_path: Объект.Строки", "children: []"} {
		if !strings.Contains(res.YAML, want) {
			t.Errorf("YAML без %q:\n%s", want, res.YAML)
		}
	}
	if info := res.Model[res.SelectedID]; !info.Container {
		t.Errorf("ТЧ должна быть container: id=%q info=%+v", res.SelectedID, info)
	}
}

// toggle колонки ТЧ (включение): insert kind:Колонка в children ТЧ даёт колонку
// с data_path, видимую в модели и на холсте (follow-up #164, слайс D2).
func TestApplyEditOp_InsertColumn(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТабТовары
    data_path: Объект.Товары
    children: []
`
	res, err := applyEditOp([]byte(src), editOpRequest{
		Op: "insert", Parent: "elements.0", Index: 0, Kind: "Колонка",
		Name: "КолНоменклатура", DataPath: "Объект.Товары.Номенклатура",
	})
	if err != nil {
		t.Fatalf("applyEditOp insert column: %v", err)
	}
	for _, want := range []string{"kind: Колонка", "data_path: Объект.Товары.Номенклатура"} {
		if !strings.Contains(res.YAML, want) {
			t.Errorf("YAML без %q:\n%s", want, res.YAML)
		}
	}
	if res.SelectedID != "elements.0.children.0" {
		t.Errorf("selectedId = %q, ожидался elements.0.children.0", res.SelectedID)
	}
	if !strings.Contains(res.CanvasHTML, `data-node-id="elements.0.children.0"`) {
		t.Errorf("колонка не отрисована на холсте:\n%s", res.CanvasHTML)
	}
}

// Числовое свойство (width) пишется в YAML числом, а не строкой — иначе декод
// FormElement.Width упал бы (follow-up #164, batch A).
func TestApplyEditOp_NumericProp(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Счёт
elements:
  - kind: ПолеКартинки
    name: Лого
`
	res, err := applyEditOp([]byte(src), editOpRequest{
		Op: "setProp", Node: "elements.0", Key: "width", Value: "120",
	})
	if err != nil {
		t.Fatalf("applyEditOp setProp width: %v", err)
	}
	if !strings.Contains(res.YAML, "width: 120") {
		t.Errorf("width должен быть числом 120:\n%s", res.YAML)
	}
	if strings.Contains(res.YAML, `width: "120"`) {
		t.Errorf("width записан строкой:\n%s", res.YAML)
	}
	if info := res.Model["elements.0"]; info.Width != 120 {
		t.Errorf("в модели width=%d, ожидался 120", info.Width)
	}
}

// move через эндпоинт меняет порядок соседей. Индекс — как его шлёт клиент при
// «↓ Ниже» (finalIdx+1 = srcIdx+2 из-за компенсации сдвига в Move), follow-up
// #164 слайс B2.
func TestApplyEditOp_Move(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op: "move", Node: "elements.0.children.0", Parent: "elements.0", Index: 2,
	})
	if err != nil {
		t.Fatalf("applyEditOp move: %v", err)
	}
	iDate := strings.Index(res.YAML, "ПолеДата")
	iNum := strings.Index(res.YAML, "ПолеНомер")
	if iDate < 0 || iNum < 0 || iDate > iNum {
		t.Errorf("порядок не изменился — ПолеДата должно стоять раньше ПолеНомер:\n%s", res.YAML)
	}
	if res.SelectedID != "" {
		t.Errorf("после move сервер сбрасывает выделение, got %q", res.SelectedID)
	}
}

// move между контейнерами (drag-перенос на холсте): поле уходит из группы на
// верхний уровень; модель отражает новое расположение (follow-up #164).
func TestApplyEditOp_MoveCrossContainer(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op: "move", Node: "elements.0.children.0", Parent: "", Index: 1,
	})
	if err != nil {
		t.Fatalf("applyEditOp move cross-container: %v", err)
	}
	if _, ok := res.Model["elements.1"]; !ok {
		t.Errorf("перенесённое поле не появилось на верхнем уровне")
	}
	if _, ok := res.Model["elements.0.children.1"]; ok {
		t.Errorf("в группе должно остаться одно поле, а есть children.1")
	}
}

// Невалидный YAML, неизвестная операция и устаревший node-id — штатные ошибки,
// без паники (план 149: баннер/конфликт на клиенте).
func TestApplyEditOp_Errors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		req  editOpRequest
	}{
		{"битый YAML", "form:\n\tbad: 1\n", editOpRequest{Op: "setProp", Node: "elements.0", Key: "name", Value: "x"}},
		{"неизвестная операция", canvasSample, editOpRequest{Op: "frobnicate"}},
		{"устаревший узел", canvasSample, editOpRequest{Op: "setProp", Node: "elements.7", Key: "name", Value: "x"}},
		{"insert без kind", canvasSample, editOpRequest{Op: "insert", Parent: "elements.0", Index: 0}},
		{"delete без node", canvasSample, editOpRequest{Op: "delete"}},
		{"delete устаревшего узла", canvasSample, editOpRequest{Op: "delete", Node: "elements.7"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := applyEditOp([]byte(tc.yaml), tc.req); err == nil {
				t.Errorf("ожидалась ошибка для %q, получено nil", tc.name)
			}
		})
	}
}

// render — перезагрузка холста из YAML без мутаций: YAML не меняется, холст и
// модель свойств отдаются, выделение сохраняется по присланному node-id.
func TestApplyEditOp_Render(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{Op: "render", Node: "elements.0.children.0"})
	if err != nil {
		t.Fatalf("applyEditOp render: %v", err)
	}
	if res.SelectedID != "elements.0.children.0" {
		t.Errorf("render не сохранил выделение: %q", res.SelectedID)
	}
	info, ok := res.Model["elements.0.children.0"]
	if !ok {
		t.Fatalf("в модели нет элемента: %v", res.Model)
	}
	if info.DataPath != "Объект.Номер" || !info.Required {
		t.Errorf("модель свойств неверна: %+v", info)
	}
	if g := res.Model["elements.0"]; !g.Container {
		t.Errorf("группа должна быть container: %+v", g)
	}
}

// setProp события элемента: events.Нажатие пишется как вложенный ключ events:,
// модель отдаёт его в Events; delProp убирает (follow-up #164 batch B1).
func TestApplyEditOp_ElementEvents(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: Кнопка
    name: КнопкаОК
`
	res, err := applyEditOp([]byte(src), editOpRequest{
		Op: "setProp", Node: "elements.0", Key: "events.Нажатие", Value: "Обработать",
	})
	if err != nil {
		t.Fatalf("applyEditOp setProp event: %v", err)
	}
	if !strings.Contains(res.YAML, "events:") || !strings.Contains(res.YAML, "Нажатие: Обработать") {
		t.Errorf("событие не записано в YAML:\n%s", res.YAML)
	}
	if res.Model["elements.0"].Events["Нажатие"] != "Обработать" {
		t.Errorf("событие не в модели: %+v", res.Model["elements.0"])
	}
	// delProp убирает обработчик.
	res2, err := applyEditOp([]byte(res.YAML), editOpRequest{
		Op: "delProp", Node: "elements.0", Key: "events.Нажатие",
	})
	if err != nil {
		t.Fatalf("applyEditOp delProp: %v", err)
	}
	if strings.Contains(res2.YAML, "Нажатие: Обработать") {
		t.Errorf("событие не удалено:\n%s", res2.YAML)
	}
}

// setOptions набора значений Переключателя: число пишется числом, представление
// попадает в модель и на холст (follow-up #164 batch C1).
func TestApplyEditOp_SetOptions(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: Переключатель
    name: Приоритет
    data_path: Объект.Приоритет
`
	res, err := applyEditOp([]byte(src), editOpRequest{
		Op: "setOptions", Node: "elements.0",
		Options: `[{"value":"1","label":"Низкий"},{"value":"2","label":"Высокий"}]`,
	})
	if err != nil {
		t.Fatalf("applyEditOp setOptions: %v", err)
	}
	if !strings.Contains(res.YAML, "value: 1") || strings.Contains(res.YAML, `value: "1"`) {
		t.Errorf("числовое значение опции должно быть числом:\n%s", res.YAML)
	}
	info := res.Model["elements.0"]
	if len(info.Options) != 2 || info.Options[1].Label != "Высокий" || info.Options[1].Value != "2" {
		t.Errorf("опции не в модели: %+v", info.Options)
	}
	if !strings.Contains(res.CanvasHTML, "Высокий") {
		t.Errorf("опции не отрисованы на холсте:\n%s", res.CanvasHTML)
	}
}

// Свойства формы (псевдо-узел "form"): title.ru и kind правятся внутри блока
// form:, события формы — на верхнем уровне; всё адресуется node="form"
// (follow-up #164 batch B2).
func TestApplyEditOp_FormProps(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op: "setProp", Node: "form", Key: "title.ru", Value: "Карточка звонка",
	})
	if err != nil {
		t.Fatalf("applyEditOp form title: %v", err)
	}
	if res.Form.TitleRU != "Карточка звонка" {
		t.Errorf("form.titleRu = %q", res.Form.TitleRU)
	}
	// kind правится внутри form:.
	res, err = applyEditOp([]byte(res.YAML), editOpRequest{
		Op: "setProp", Node: "form", Key: "kind", Value: "list",
	})
	if err != nil {
		t.Fatalf("applyEditOp form kind: %v", err)
	}
	if res.Form.Kind != "list" {
		t.Errorf("form.kind = %q", res.Form.Kind)
	}
	// Событие формы уходит на верхний уровень (рядом с form/elements), не внутрь form.
	res, err = applyEditOp([]byte(res.YAML), editOpRequest{
		Op: "setProp", Node: "form", Key: "events.ПриОткрытии", Value: "ПриОткрытииФормы",
	})
	if err != nil {
		t.Fatalf("applyEditOp form event: %v", err)
	}
	if res.Form.Events["ПриОткрытии"] != "ПриОткрытииФормы" {
		t.Errorf("form.events = %v", res.Form.Events)
	}
	iEvents := strings.Index(res.YAML, "\nevents:")
	iElements := strings.Index(res.YAML, "\nelements:")
	if iEvents < 0 || iElements < 0 {
		t.Fatalf("нет блоков events/elements верхнего уровня:\n%s", res.YAML)
	}
}

// Штатное действие формы: actions.delete.visible=false пишется булевым скаляром
// на верхний уровень, модель отдаёт actions.delete=false (follow-up #164 B3).
func TestApplyEditOp_FormActionVisible(t *testing.T) {
	res, err := applyEditOp([]byte(canvasSample), editOpRequest{
		Op: "setProp", Node: "form", Key: "actions.delete.visible", Value: "false",
	})
	if err != nil {
		t.Fatalf("applyEditOp form action: %v", err)
	}
	if !strings.Contains(res.YAML, "visible: false") {
		t.Errorf("visible должен быть булевым скаляром:\n%s", res.YAML)
	}
	if v, ok := res.Form.Actions["delete"]; !ok || v {
		t.Errorf("form.actions[delete] = %v (ok=%v), ожидалось false", v, ok)
	}
}

// Round-trip эндпоинта сохраняет ручной комментарий пользователя — ключевое
// требование #164 (правка свойства не затирает аннотации).
func TestApplyEditOp_PreservesComments(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: ГруппаФормы
    name: Группа1   # основная группа
    children:
      - kind: ПолеВвода
        name: Поле1
        data_path: Объект.Дата
`
	res, err := applyEditOp([]byte(src), editOpRequest{
		Op: "setProp", Node: "elements.0.children.0", Key: "required", Value: "true",
	})
	if err != nil {
		t.Fatalf("applyEditOp: %v", err)
	}
	if !strings.Contains(res.YAML, "# основная группа") {
		t.Errorf("потерян ручной комментарий:\n%s", res.YAML)
	}
}

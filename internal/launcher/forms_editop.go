package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/formdoc"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Эндпоинт визуальной правки формы (#164, слайс 5).
//
// Холст серверо-центричен: правка на клиенте превращается в команду
// {op, target, value}, уходит сюда; сервер хирургически меняет дерево
// yaml.Node, пере-сериализует YAML и пере-рендерит холст, возвращая
// {yaml, canvasHtml, selectedId, errors}. Источник истины — текст YAML из
// редактора: его присылает клиент, мы его правим и отдаём обратно — Monaco и
// холст синхронизируются от одного результата. Состояние на сервере не копится.

// editOpRequest — разобранная команда правки формы.
type editOpRequest struct {
	Op       string // render | setProp | delProp | insert | move | delete | setOptions
	Node     string // node-id цели (setProp, move); "form" = корневые свойства формы
	Key      string // setProp/delProp: имя свойства (может быть вложенным, "title.ru")
	Value    string // setProp: значение (сырое; bool-свойства приводятся)
	Parent   string // insert/move: node-id контейнера ("" = верхний уровень)
	Index    int    // insert/move: позиция в контейнере
	Kind     string // insert: вид нового элемента
	Name     string // insert: имя нового элемента
	DataPath string // insert: data_path нового элемента
	TitleRU  string // insert: ru-заголовок нового элемента
	Options  string // setOptions: JSON-массив [{value,label}] набора значений
}

// editOpResult — результат применения команды к YAML.
type editOpResult struct {
	YAML       string
	CanvasHTML string
	SelectedID string
	Model      map[string]canvasElementInfo
	Form       formInfo
}

// formInfo — корневые свойства формы для панели «Свойства формы» (batch B2/B3).
type formInfo struct {
	TitleRU string            `json:"titleRu"`
	Kind    string            `json:"kind"`
	Events  map[string]string `json:"events"`
	Actions map[string]bool   `json:"actions"`
}

// boolProps — свойства элемента, значение которых интерпретируется как bool
// (чекбоксы панели свойств шлют "true"/"" — пишем в YAML булев скаляр, не строку).
var boolProps = map[string]bool{
	"required": true, "readonly": true, "choice": true,
	"visible": true, "enabled": true, "no_grid": true,
}

// numProps — целочисленные свойства: пишем в YAML числом, а не строкой (иначе
// декод FormElement.Width/Height упадёт). Пустая строка → 0.
var numProps = map[string]bool{"width": true, "height": true}

// coercePropValue приводит сырое строковое значение свойства к типу: bool для
// чекбокс-свойств, иначе — строка как есть.
func coercePropValue(key, raw string) any {
	leaf := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		leaf = key[i+1:]
	}
	if boolProps[leaf] {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "1", "on", "yes", "да":
			return true
		default:
			return false
		}
	}
	if numProps[leaf] {
		n, _ := strconv.Atoi(strings.TrimSpace(raw))
		return n
	}
	return raw
}

// coerceOptionValue приводит значение опции набора к числу, если оно
// числовое (чтобы YAML был чистым: value: 1, а не "1"), иначе оставляет строкой.
// Сохранение значения в БД от типа в YAML не зависит — оно приводится по типу
// поля сущности (formToFields), но числовое поле читается естественнее (C1).
func coerceOptionValue(raw string) any {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if n, err := strconv.Atoi(s); err == nil && strconv.Itoa(n) == s {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// setFormProp/delFormProp маршрутизируют правку псевдо-узла "form": заголовок и
// вид формы лежат внутри блока form:, а события и действия уровня формы — в
// верхнем mapping-узле (см. formYAMLDoc). Клиент шлёт всё как node="form".
func setFormProp(doc *formdoc.Doc, key string, value any) error {
	if strings.HasPrefix(key, "events.") || strings.HasPrefix(key, "actions.") {
		return doc.SetTopProp(key, value)
	}
	return doc.SetProp("form", key, value)
}

func delFormProp(doc *formdoc.Doc, key string) error {
	if strings.HasPrefix(key, "events.") || strings.HasPrefix(key, "actions.") {
		return doc.DeleteTopProp(key)
	}
	return doc.DeleteProp("form", key)
}

// applyEditOp применяет команду к YAML-исходнику формы и возвращает обновлённые
// YAML + HTML холста + node-id выделения. Чистая функция (без HTTP) — ядро
// эндпоинта, покрытое юнит-тестами.
func applyEditOp(yamlSrc []byte, req editOpRequest) (editOpResult, error) {
	doc, err := formdoc.Load(yamlSrc)
	if err != nil {
		return editOpResult{}, fmt.Errorf("YAML формы не разобран: %w", err)
	}

	var selected string
	switch req.Op {
	case "render":
		// Перезагрузка холста из текущего YAML без мутаций (направление
		// YAML→холст). Выделение сохраняется по присланному node-id.
		selected = req.Node

	case "setProp":
		if req.Key == "" {
			return editOpResult{}, fmt.Errorf("setProp: пустой key")
		}
		val := coercePropValue(req.Key, req.Value)
		if req.Node == "form" {
			err = setFormProp(doc, req.Key, val)
		} else {
			err = doc.SetProp(req.Node, req.Key, val)
		}
		if err != nil {
			return editOpResult{}, err
		}
		selected = req.Node

	case "delProp":
		if req.Key == "" {
			return editOpResult{}, fmt.Errorf("delProp: пустой key")
		}
		if req.Node == "form" {
			err = delFormProp(doc, req.Key)
		} else {
			err = doc.DeleteProp(req.Node, req.Key)
		}
		if err != nil {
			return editOpResult{}, err
		}
		selected = req.Node

	case "setOptions":
		var raw []struct {
			Value string `json:"value"`
			Label string `json:"label"`
		}
		if err := json.Unmarshal([]byte(req.Options), &raw); err != nil {
			return editOpResult{}, fmt.Errorf("setOptions: разбор JSON: %w", err)
		}
		opts := make([]formdoc.Option, 0, len(raw))
		for _, r := range raw {
			o := formdoc.Option{Value: coerceOptionValue(r.Value)}
			if strings.TrimSpace(r.Label) != "" {
				o.Label = map[string]string{"ru": r.Label}
			}
			opts = append(opts, o)
		}
		if err := doc.SetOptions(req.Node, opts); err != nil {
			return editOpResult{}, err
		}
		selected = req.Node

	case "insert":
		if strings.TrimSpace(req.Kind) == "" {
			return editOpResult{}, fmt.Errorf("insert: не указан kind")
		}
		fields := map[string]any{"kind": req.Kind}
		if req.Name != "" {
			fields["name"] = req.Name
		}
		if req.DataPath != "" {
			fields["data_path"] = req.DataPath
		}
		if req.TitleRU != "" {
			fields["title"] = map[string]string{"ru": req.TitleRU}
		}
		// Контейнеры (группа/страницы/страница) создаём с пустым children —
		// YAML сразу структурно явный, а на холсте внутри появляется drop-зона
		// (follow-up #164, слайс C).
		if (&metadata.FormElement{Kind: metadata.FormElementType(req.Kind)}).IsContainer() {
			fields["children"] = []any{}
		}
		newID, err := doc.InsertElement(req.Parent, req.Index, fields)
		if err != nil {
			return editOpResult{}, err
		}
		selected = newID

	case "move":
		if err := doc.Move(req.Node, req.Parent, req.Index); err != nil {
			return editOpResult{}, err
		}
		// Индексы после переноса смещаются — клиент перезапрашивает выделение.
		selected = ""

	case "delete":
		if strings.TrimSpace(req.Node) == "" {
			return editOpResult{}, fmt.Errorf("delete: пустой node")
		}
		if err := doc.DeleteElement(req.Node); err != nil {
			return editOpResult{}, err
		}
		// Узел удалён вместе с поддеревом — выделение сбрасывается, индексы
		// соседей сместились (клиент пере-рендерит холст без выделения).
		selected = ""

	default:
		return editOpResult{}, fmt.Errorf("неизвестная операция %q", req.Op)
	}

	out, err := doc.Bytes()
	if err != nil {
		return editOpResult{}, err
	}
	canvas, err := renderFormCanvas(doc, selected)
	if err != nil {
		return editOpResult{}, err
	}
	model, err := canvasModel(doc)
	if err != nil {
		return editOpResult{}, err
	}
	meta, err := doc.FormMeta()
	if err != nil {
		return editOpResult{}, err
	}
	return editOpResult{
		YAML:       string(out),
		CanvasHTML: canvas,
		SelectedID: selected,
		Model:      model,
		Form:       formInfo{TitleRU: meta.TitleRU, Kind: meta.Kind, Events: meta.Events, Actions: meta.Actions},
	}, nil
}

// editOpResponse — JSON-ответ эндпоинта.
type editOpResponse struct {
	OK         bool                         `json:"ok"`
	YAML       string                       `json:"yaml,omitempty"`
	CanvasHTML string                       `json:"canvasHtml,omitempty"`
	SelectedID string                       `json:"selectedId,omitempty"`
	Model      map[string]canvasElementInfo `json:"model,omitempty"`
	Form       *formInfo                    `json:"form,omitempty"`
	Errors     []string                     `json:"errors,omitempty"`
}

// configuratorFormsEditOp — POST: применяет визуальную команду к YAML формы и
// возвращает синхронизированные YAML + холст. Состояние не пишется на диск —
// сохранение по-прежнему через /forms/save.
func (h *handler) configuratorFormsEditOp(w http.ResponseWriter, r *http.Request) {
	if _, err := h.store.Get(chi.URLParam(r, "id")); err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		writeFormsJSON(w, editOpResponse{OK: false, Errors: []string{err.Error()}})
		return
	}
	index, _ := strconv.Atoi(r.FormValue("index"))
	req := editOpRequest{
		Op:       strings.TrimSpace(r.FormValue("op")),
		Node:     r.FormValue("node"),
		Key:      r.FormValue("key"),
		Value:    r.FormValue("value"),
		Parent:   r.FormValue("parent"),
		Index:    index,
		Kind:     r.FormValue("kind"),
		Name:     r.FormValue("name"),
		DataPath: r.FormValue("data_path"),
		TitleRU:  r.FormValue("title_ru"),
		Options:  r.FormValue("options"),
	}
	res, err := applyEditOp([]byte(r.FormValue("yaml")), req)
	if err != nil {
		writeFormsJSON(w, editOpResponse{OK: false, Errors: []string{err.Error()}})
		return
	}
	form := res.Form
	writeFormsJSON(w, editOpResponse{
		OK:         true,
		YAML:       res.YAML,
		CanvasHTML: res.CanvasHTML,
		SelectedID: res.SelectedID,
		Model:      res.Model,
		Form:       &form,
	})
}

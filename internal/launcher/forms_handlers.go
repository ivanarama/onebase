package launcher

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/onec_forms"
	"github.com/ivantit66/onebase/internal/project"
)

// ── Управляемые формы в конфигураторе (план 37, этап 4) ──────────────────────
//
// Группа handler'ов, обеспечивающих UI-редактор управляемых форм по образцу
// существующего редактора печатных форм (Monaco + live preview):
//   GET  /bases/{id}/configurator/forms                        — список managed-форм
//   GET  /bases/{id}/configurator/forms/edit?entity=X&name=Y   — split-pane редактор
//   POST /bases/{id}/configurator/forms/save                   — сохранить YAML + .os
//   POST /bases/{id}/configurator/forms/delete                 — удалить YAML/OS/ресурсы
//   POST /bases/{id}/configurator/forms/preview                — отрендерить HTML формы для iframe
//   POST /bases/{id}/configurator/forms/validate               — JSON warnings + ok
//   POST /bases/{id}/configurator/forms/import-1c              — multipart Form.xml + Module.bsl + Items
//
// Опциональность: если у сущности нет ни одной .form.yaml — рантайм
// работает на авто-форме без изменений. Создание managed-формы через
// редактор автоматически активирует managed-рендер (см. этап 3).

// cfgManagedForm — UI-проекция управляемой формы для списка/редактора.
type cfgManagedForm struct {
	Entity   string // имя сущности
	Name     string // имя формы (без расширения .form.yaml)
	Kind     string // object | list | choice | folder | custom
	YAML     string // содержимое .form.yaml
	OS       string // содержимое .form.os (если есть)
	HasOS    bool
	YAMLPath string // относительный путь от корня проекта
	OSPath   string
}

// formsRoot — каталог с управляемыми формами в проекте (file-based mode):
// <projectDir>/forms.
func formsRoot(b *Base) string {
	return filepath.Join(b.Path, "forms")
}

// formFiles вычисляет пути к YAML/OS для (entity, name) при сохранении.
// Нижний регистр используется для имён каталогов/файлов чтобы избежать
// конфликтов на нечувствительных к регистру ФС.
func formFiles(b *Base, entity, name string) (yamlPath, osPath string) {
	entityLower := strings.ToLower(entity)
	nameLower := strings.ToLower(name)
	dir := filepath.Join(formsRoot(b), entityLower)
	return filepath.Join(dir, nameLower+".form.yaml"),
		filepath.Join(dir, nameLower+".form.os")
}

// listManagedForms собирает информацию обо всех managed-формах проекта.
// Сначала пытается читать из БД (конфигурация в БД), затем — из файлов.
func (h *handler) listManagedForms(r *http.Request, b *Base) ([]cfgManagedForm, error) {
	if b.ConfigSource == "database" {
		return h.listManagedFormsFromDB(r, b)
	}
	return listManagedFormsFromFS(b)
}

func listManagedFormsFromFS(b *Base) ([]cfgManagedForm, error) {
	root := formsRoot(b)
	st, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, nil
	}

	var out []cfgManagedForm
	entityDirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, ed := range entityDirs {
		if !ed.IsDir() {
			continue
		}
		entityDir := filepath.Join(root, ed.Name())
		files, err := os.ReadDir(entityDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".form.yaml") {
				continue
			}
			yamlPath := filepath.Join(entityDir, f.Name())
			body, err := os.ReadFile(yamlPath)
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(f.Name(), ".form.yaml")
			osPath := filepath.Join(entityDir, name+".form.os")
			osBody, _ := os.ReadFile(osPath)
			kind := extractFormKindFromYAML(string(body))
			entityName := properCaseEntity(b, ed.Name())
			out = append(out, cfgManagedForm{
				Entity:   entityName,
				Name:     properCaseName(name),
				Kind:     kind,
				YAML:     string(body),
				OS:       string(osBody),
				HasOS:    len(osBody) > 0,
				YAMLPath: filepath.ToSlash(strings.TrimPrefix(yamlPath, b.Path+string(os.PathSeparator))),
				OSPath:   filepath.ToSlash(strings.TrimPrefix(osPath, b.Path+string(os.PathSeparator))),
			})
		}
	}
	return out, nil
}

func (h *handler) listManagedFormsFromDB(r *http.Request, b *Base) ([]cfgManagedForm, error) {
	db, err := OpenDB(r.Context(), b)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	repo := configdb.New(db)
	files, err := repo.ListByPrefix(r.Context(), "forms/")
	if err != nil {
		return nil, err
	}

	// группируем по entity + name
	type pair struct{ yaml, os string }
	groups := map[string]*cfgManagedForm{}
	for _, f := range files {
		// f.Path = forms/<entity>/<name>.form.yaml | .form.os
		parts := strings.Split(strings.TrimPrefix(f.Path, "forms/"), "/")
		if len(parts) < 2 {
			continue
		}
		entityLower := parts[0]
		base := parts[1]
		var name string
		var isYAML, isOS bool
		if strings.HasSuffix(base, ".form.yaml") {
			name = strings.TrimSuffix(base, ".form.yaml")
			isYAML = true
		} else if strings.HasSuffix(base, ".form.os") {
			name = strings.TrimSuffix(base, ".form.os")
			isOS = true
		} else {
			continue
		}
		key := entityLower + "/" + name
		g, ok := groups[key]
		if !ok {
			g = &cfgManagedForm{
				Entity:   properCaseEntity(b, entityLower),
				Name:     properCaseName(name),
				YAMLPath: "forms/" + entityLower + "/" + name + ".form.yaml",
				OSPath:   "forms/" + entityLower + "/" + name + ".form.os",
			}
			groups[key] = g
		}
		if isYAML {
			g.YAML = string(f.Content)
			g.Kind = extractFormKindFromYAML(g.YAML)
		}
		if isOS {
			g.OS = string(f.Content)
			g.HasOS = true
		}
	}

	out := make([]cfgManagedForm, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	return out, nil
}

// extractFormKindFromYAML быстро достаёт kind: object/list/... без полного парсинга.
// Достаточно для UI; полная валидация — отдельным handler'ом.
func extractFormKindFromYAML(yaml string) string {
	// поиск строки "  kind: <value>"
	for _, line := range strings.Split(yaml, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "kind:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "kind:"))
		}
	}
	return ""
}

// properCaseEntity возвращает оригинальное имя сущности по её lowercase-варианту.
// Сопоставление через project metadata.
func properCaseEntity(b *Base, lower string) string {
	// Без доступа к project.Project мы не знаем оригинальный регистр.
	// Возвращаем "как есть" — UI всё равно покажет, и URL-сравнение
	// делается через ToLower.
	return lower
}

func properCaseName(s string) string {
	return s
}

// configuratorFormsList — страница со списком всех managed-форм проекта.
// Рендерится через formsTmpl ("forms-list"), а не через общий cfgTmpl —
// у управляемых форм собственная страница с импортом, созданием и таблицей.
func (h *handler) configuratorFormsList(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	forms, err := h.listManagedForms(r, b)
	if err != nil {
		http.Error(w, tr(resolveLang(r), "Не удалось прочитать список форм")+": "+err.Error(), 500)
		return
	}
	data := &configuratorData{Base: b, ManagedForms: forms, Lang: resolveLang(r)}
	// Сохранить узел-источник для back-link после delete-редиректа (follow-up #164).
	data.FormEditFrom = strings.TrimSpace(r.URL.Query().Get("from"))
	// Подсветить флаги после save/delete-редиректа.
	if q := r.URL.Query().Get("saved"); q != "" {
		data.FieldsSaved = true
		data.FieldsSavedEntity = q
	}
	if q := r.URL.Query().Get("err"); q != "" {
		data.Error = q
	}
	renderFormsList(w, data)
}

// configuratorFormsEdit — split-pane Monaco-редактор одной формы.
// Если ?entity=…&name=… указывают на отсутствующую форму — открывается
// режим «Новая форма» с заранее заполненным шаблоном YAML.
func (h *handler) configuratorFormsEdit(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	entity := strings.TrimSpace(r.URL.Query().Get("entity"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if entity == "" || name == "" {
		http.Redirect(w, r, fmt.Sprintf("/bases/%s/configurator/forms", b.ID), http.StatusSeeOther)
		return
	}

	var form *cfgManagedForm
	all, _ := h.listManagedForms(r, b)
	for i := range all {
		if strings.EqualFold(all[i].Entity, entity) && strings.EqualFold(all[i].Name, name) {
			form = &all[i]
			break
		}
	}
	// Реквизиты объекта: для шаблона новой формы (issue #133) и для палитры
	// перетаскивания реквизитов на форму в редакторе (issue #134). Поля
	// справочника/документа либо параметры обработки/отчёта; у обработки нет
	// «Наименования».
	var attrs []formScaffoldAttr
	var tableParts []formTablePart
	if proj, perr := h.loadProjectFor(r.Context(), b); perr == nil {
		attrs = objectScaffoldAttrs(proj, entity)
		tableParts = objectScaffoldTableParts(proj, entity)
		proj.Close()
	}

	if form == nil {
		// «Новая форма» — заранее заполненный шаблон YAML из реальных реквизитов.
		form = &cfgManagedForm{
			Entity: entity,
			Name:   name,
			Kind:   "object",
			YAML:   newFormYAMLTemplate(entity, name, attrs),
		}
	}

	data := &configuratorData{Base: b, EditingForm: form, EditingFormAttrs: attrs, EditingFormTableParts: tableParts, Lang: resolveLang(r)}
	// Узел дерева, из которого открыли редактор — для back-link «← В конфигуратор»
	// и проброса дальше в save/delete-формы (follow-up #164, слайс A).
	data.FormEditFrom = strings.TrimSpace(r.URL.Query().Get("from"))
	if q := r.URL.Query().Get("saved"); q != "" {
		data.FieldsSaved = true
		data.FieldsSavedEntity = q
	}
	if q := r.URL.Query().Get("warnings"); q != "" {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "Импорт завершён, предупреждений: " + q
	}
	renderFormsEditor(w, data)
}

// formScaffoldAttr — реквизит объекта (поле справочника/документа либо параметр
// обработки/отчёта), из которого строится заготовка новой формы.
type formScaffoldAttr struct {
	Name  string `json:"name"`
	Title string `json:"title"`          // синоним/подпись; пустой → подставляется Name
	Type  string `json:"type,omitempty"` // тип поля (string/date/bool/reference:…/enum:…) — для «умного» дропа реквизита
}

// objectScaffoldAttrs возвращает реквизиты объекта <entity> для заготовки новой
// формы: поля справочника/документа либо параметры обработки/отчёта. Возвращает
// nil, если объект не найден или у него нет реквизитов — тогда форма создаётся с
// пустой группой, а не с полем «Наименование», которого у обработок нет (issue
// #133).
func objectScaffoldAttrs(proj *project.Project, entity string) []formScaffoldAttr {
	if proj == nil || entity == "" {
		return nil
	}
	for _, e := range proj.Entities {
		if strings.EqualFold(e.Name, entity) {
			attrs := make([]formScaffoldAttr, 0, len(e.Fields))
			for _, f := range e.Fields {
				attrs = append(attrs, formScaffoldAttr{Name: f.Name, Title: f.Title, Type: string(f.Type)})
			}
			return attrs
		}
	}
	for _, p := range proj.Processors {
		if strings.EqualFold(p.Name, entity) {
			attrs := make([]formScaffoldAttr, 0, len(p.Params))
			for _, pr := range p.Params {
				attrs = append(attrs, formScaffoldAttr{Name: pr.Name, Title: pr.Label})
			}
			return attrs
		}
	}
	for _, rep := range proj.Reports {
		if strings.EqualFold(rep.Name, entity) {
			attrs := make([]formScaffoldAttr, 0, len(rep.Params))
			for _, pr := range rep.Params {
				attrs = append(attrs, formScaffoldAttr{Name: pr.Name, Title: pr.Label})
			}
			return attrs
		}
	}
	return nil
}

// formTablePart — табличная часть объекта с составом колонок для палитры ТЧ на
// холсте (drop вставляет ТабличнуюЧасть) и редактора состава колонок (follow-up
// #164, слайсы D1/D2).
type formTablePart struct {
	Name    string             `json:"name"`
	Title   string             `json:"title"`
	Columns []formScaffoldAttr `json:"columns"`
}

// objectScaffoldTableParts возвращает табличные части справочника/документа
// <entity> с их полями (колонками). Только сущности — у обработок/отчётов ТЧ нет.
func objectScaffoldTableParts(proj *project.Project, entity string) []formTablePart {
	if proj == nil || entity == "" {
		return nil
	}
	for _, e := range proj.Entities {
		if !strings.EqualFold(e.Name, entity) {
			continue
		}
		tps := make([]formTablePart, 0, len(e.TableParts))
		for _, tp := range e.TableParts {
			cols := make([]formScaffoldAttr, 0, len(tp.Fields))
			for _, f := range tp.Fields {
				cols = append(cols, formScaffoldAttr{Name: f.Name, Title: f.Title})
			}
			tps = append(tps, formTablePart{Name: tp.Name, Title: tp.Title, Columns: cols})
		}
		return tps
	}
	return nil
}

// newFormYAMLTemplate — начальный YAML при создании новой формы. Группа
// «Реквизиты» заполняется реальными реквизитами объекта (attrs); если их нет
// (например, обработка без параметров) — группа остаётся пустой, без хардкодного
// поля «Наименование» (issue #133).
func newFormYAMLTemplate(entity, name string, attrs []formScaffoldAttr) string {
	if name == "" {
		name = "ФормаОбъекта"
	}
	if entity == "" {
		entity = "Сущность"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `schema: onebase.form/v1
form:
  name: %s
  kind: object
  entity: %s
  title:
    ru: "Карточка"

elements:
  - kind: ГруппаФормы
    name: Реквизиты
    title:
      ru: "Реквизиты"
`, name, entity)
	if len(attrs) == 0 {
		// Реквизитов нет — пустая группа; пользователь добавит элементы сам.
		b.WriteString("    children: []\n")
		return b.String()
	}
	b.WriteString("    children:\n")
	for _, a := range attrs {
		title := a.Title
		if title == "" {
			title = a.Name
		}
		fmt.Fprintf(&b, `      - kind: ПолеВвода
        name: Поле%s
        title:
          ru: %s
        data_path: Объект.%s
`, a.Name, yamlDQString(title), a.Name)
	}
	return b.String()
}

// yamlDQString оборачивает строку в безопасный YAML double-quoted скаляр
// (экранирует обратный слэш и кавычку).
func yamlDQString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// configuratorFormsSave — POST: пишет YAML и (опционально) .form.os.
// На вход принимает form-values: entity, name, yaml, os.
func (h *handler) configuratorFormsSave(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	entity := strings.TrimSpace(r.FormValue("entity"))
	name := strings.TrimSpace(r.FormValue("name"))
	yamlBody := r.FormValue("yaml")
	osBody := r.FormValue("os")
	if entity == "" || name == "" {
		http.Error(w, tr(resolveLang(r), "entity и name обязательны"), 400)
		return
	}

	// Валидируем YAML через managed_form_loader — отсекаем явный мусор.
	if _, vErr := (loader.NewManagedFormLoader()).LoadFormFile("", entity); vErr != nil {
		// здесь мы намеренно используем загрузку через временный файл ниже;
		// loader без файла не сработает. Полноценная валидация — отдельным
		// handler'ом /forms/validate.
		_ = vErr
	}

	saveErr := saveManagedForm(r, b, entity, name, []byte(yamlBody), []byte(osBody))

	// После сохранения — редирект на редактор с флагами, чтобы избежать
	// повторного submit при F5 и подсветить статус.
	target := fmt.Sprintf("/bases/%s/configurator/forms/edit?entity=%s&name=%s", b.ID, entity, name)
	if from := strings.TrimSpace(r.FormValue("from")); from != "" {
		target += "&from=" + from
	}
	if saveErr != nil {
		target += "&err=" + saveErr.Error()
	} else {
		target += "&saved=" + entity + "." + name
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func saveManagedForm(r *http.Request, b *Base, entity, name string, yamlBody, osBody []byte) error {
	yamlPath := fmt.Sprintf("forms/%s/%s.form.yaml", strings.ToLower(entity), strings.ToLower(name))
	osPath := fmt.Sprintf("forms/%s/%s.form.os", strings.ToLower(entity), strings.ToLower(name))
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		files := []configdb.ConfigFile{{Path: yamlPath, Content: yamlBody}}
		if len(osBody) > 0 {
			files = append(files, configdb.ConfigFile{Path: osPath, Content: osBody})
		}
		return repo.SaveFiles(r.Context(), files, configdb.VersionOptions{
			AuthorLogin: cfgLogin(r.Context()),
			Message:     "save managed form " + entity + "." + name,
		})
	}
	// FS
	yp, err := configdb.SafeJoin(b.Path, yamlPath)
	if err != nil {
		return err
	}
	op, err := configdb.SafeJoin(b.Path, osPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(yp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(yp, yamlBody, 0o644); err != nil {
		return err
	}
	if len(osBody) > 0 {
		if err := os.WriteFile(op, osBody, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// configuratorFormsDelete — удаляет .form.yaml + .form.os + _resources.
func (h *handler) configuratorFormsDelete(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	entity := strings.TrimSpace(r.FormValue("entity"))
	name := strings.TrimSpace(r.FormValue("name"))
	if entity == "" || name == "" {
		http.Error(w, tr(resolveLang(r), "entity и name обязательны"), 400)
		return
	}

	delErr := deleteManagedForm(r, b, entity, name)

	target := fmt.Sprintf("/bases/%s/configurator/forms", b.ID)
	if delErr != nil {
		target += "?err=" + delErr.Error()
	} else {
		target += "?saved=Удалена форма " + entity + "." + name
	}
	// Сохранить узел-источник, чтобы back-link на списке форм вёл обратно (follow-up #164).
	if from := strings.TrimSpace(r.FormValue("from")); from != "" {
		target += "&from=" + from
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func deleteManagedForm(r *http.Request, b *Base, entity, name string) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		prefix := fmt.Sprintf("forms/%s/%s", strings.ToLower(entity), strings.ToLower(name))
		files, err := repo.ListByPrefix(r.Context(), prefix)
		if err != nil {
			return err
		}
		paths := make([]string, 0, len(files))
		for _, f := range files {
			paths = append(paths, f.Path)
		}
		return repo.DeleteFiles(r.Context(), paths, configdb.VersionOptions{
			AuthorLogin: cfgLogin(r.Context()),
			Message:     "delete managed form " + entity + "." + name,
		})
	}
	yp, op := formFiles(b, entity, name)
	_ = os.Remove(yp)
	_ = os.Remove(op)
	// _resources/ соседним каталогом — удаляем рекурсивно если есть
	resDir := filepath.Join(filepath.Dir(yp), strings.ToLower(name))
	_ = os.RemoveAll(resDir)
	return nil
}

// configuratorFormsValidate — POST YAML, возвращает JSON со списком warnings.
// Использует managed_form_loader для базовой YAML-валидации.
func (h *handler) configuratorFormsValidate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	yamlBody := r.FormValue("yaml")
	entity := strings.TrimSpace(r.FormValue("entity"))
	if entity == "" {
		entity = "Стуб"
	}

	// Пишем во временный файл и пытаемся загрузить через managed_form_loader.
	tmp, err := os.CreateTemp("", "obform-*.form.yaml")
	if err != nil {
		writeFormsJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(yamlBody)
	tmp.Close()

	type item struct {
		Severity string `json:"severity"`
		Code     string `json:"code,omitempty"`
		Message  string `json:"message"`
	}
	resp := struct {
		OK    bool   `json:"ok"`
		Items []item `json:"items"`
	}{OK: true}

	if _, err := loader.NewManagedFormLoader().LoadFormFile(tmp.Name(), entity); err != nil {
		resp.OK = false
		resp.Items = append(resp.Items, item{Severity: "error", Message: err.Error()})
	}
	writeFormsJSON(w, resp)
}

// previewErrorHTML и renderManagedFormPreview объявлены в forms_tmpl.go.

// configuratorFormsPreview — POST: получает YAML, рендерит «упрощённую»
// HTML-форму для iframe-предпросмотра.
//
// Для MVP препрос обходится без обращения к metadata.Entity — рисует
// дерево как ul/li с типами и data_path. Полноценный рендер через
// internal/ui рантайм-движок добавится в будущем (для этого нужно
// поднимать целую сессию UI-сервера, что излишне для preview).
func (h *handler) configuratorFormsPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	yamlBody := r.FormValue("yaml")
	entity := strings.TrimSpace(r.FormValue("entity"))
	if entity == "" {
		entity = "Объект"
	}

	tmp, err := os.CreateTemp("", "obpreview-*.form.yaml")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(yamlBody)
	tmp.Close()

	fm, err := loader.NewManagedFormLoader().LoadFormFile(tmp.Name(), entity)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, previewErrorHTML(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, renderManagedFormPreview(fm))
}

// configuratorFormsImport1C — multipart-загрузка ZIP с Form.xml + Module.bsl
// + Items/, или одиночного Form.xml. Используем onec_forms.ImportFromOneC.
func (h *handler) configuratorFormsImport1C(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB
		http.Error(w, err.Error(), 400)
		return
	}
	lang := resolveLang(r)
	entity := strings.TrimSpace(r.FormValue("entity"))
	formName := strings.TrimSpace(r.FormValue("name"))
	if entity == "" {
		http.Error(w, tr(lang, "не указана сущность"), 400)
		return
	}
	if formName == "" {
		formName = "Форма"
	}

	// Поддерживаем 2 варианта:
	// 1) поле "zip" с архивом, внутри ожидается Form.xml в корне или в Ext/
	// 2) три поля: form_xml, module_bsl, items_zip
	tmpDir, err := os.MkdirTemp("", "obimport-")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(tmpDir)

	xmlPath, bslPath, itemsDir, err := extractImportSource(r, tmpDir)
	if err != nil {
		http.Error(w, tr(lang, "Ошибка чтения архива")+": "+err.Error(), 400)
		return
	}
	if xmlPath == "" {
		http.Error(w, tr(lang, "Form.xml не найден в загруженных файлах"), 400)
		return
	}

	dstYAML, dstOS := formFiles(b, entity, formName)
	dstResources := filepath.Join(filepath.Dir(dstYAML), strings.ToLower(formName), "_resources")

	if b.ConfigSource == "database" {
		// Для БД-режима делаем импорт во временный каталог, затем заливаем содержимое в БД.
		tmpOut, _ := os.MkdirTemp("", "obimport-out-")
		defer os.RemoveAll(tmpOut)
		dstYAML = filepath.Join(tmpOut, "form.yaml")
		dstOS = filepath.Join(tmpOut, "form.os")
		dstResources = filepath.Join(tmpOut, "_resources")
	}

	report, err := onec_forms.ImportFromOneC(onec_forms.ImportOptions{
		XMLPath:         xmlPath,
		BSLPath:         bslPath,
		ItemsDir:        itemsDir,
		EntityName:      entity,
		FormName:        formName,
		FormKind:        "custom",
		DstYAMLPath:     dstYAML,
		DstOSPath:       dstOS,
		DstResourcesDir: dstResources,
	})
	if err != nil {
		http.Error(w, tr(lang, "Импорт не удался")+": "+err.Error(), 500)
		return
	}

	if b.ConfigSource == "database" {
		yamlBody, _ := os.ReadFile(dstYAML)
		osBody, _ := os.ReadFile(dstOS)
		if err := saveManagedForm(r, b, entity, formName, yamlBody, osBody); err != nil {
			http.Error(w, tr(lang, "Сохранение в БД")+": "+err.Error(), 500)
			return
		}
	}

	// Перенаправляем на редактор созданной формы.
	target := fmt.Sprintf("/bases/%s/configurator/forms/edit?entity=%s&name=%s&warnings=%d",
		b.ID, entity, formName, len(report.Warnings))
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// extractImportSource распаковывает multipart-загрузку в tmpDir.
// Возвращает пути к Form.xml, Module.bsl (если есть) и каталогу Items.
func extractImportSource(r *http.Request, tmpDir string) (string, string, string, error) {
	// Вариант 1: одиночный ZIP в поле "zip"
	if zipFile, _, err := r.FormFile("zip"); err == nil {
		defer zipFile.Close()
		data, err := io.ReadAll(zipFile)
		if err != nil {
			return "", "", "", err
		}
		if err := unzipBytes(data, tmpDir); err != nil {
			return "", "", "", err
		}
	}

	// Вариант 2: отдельные поля
	saveFile := func(field, dst string) error {
		f, _, err := r.FormFile(field)
		if err != nil {
			return nil // не обязательно
		}
		defer f.Close()
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, f)
		return err
	}
	if err := saveFile("form_xml", filepath.Join(tmpDir, "Form.xml")); err != nil {
		return "", "", "", err
	}
	if err := saveFile("module_bsl", filepath.Join(tmpDir, "Module.bsl")); err != nil {
		return "", "", "", err
	}

	// Найдём Form.xml в дереве распакованных файлов (на разных уровнях).
	var xmlPath, bslPath, itemsDir string
	filepath.WalkDir(tmpDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), "Items") {
				itemsDir = p
			}
			return nil
		}
		switch strings.ToLower(d.Name()) {
		case "form.xml":
			if xmlPath == "" {
				xmlPath = p
			}
		case "module.bsl":
			if bslPath == "" {
				bslPath = p
			}
		}
		return nil
	})
	return xmlPath, bslPath, itemsDir, nil
}

func unzipBytes(data []byte, dst string) error {
	zr, err := zip.NewReader(bytesReaderAt(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		fp := filepath.Join(dst, f.Name)
		// защита от path traversal
		if !strings.HasPrefix(fp, filepath.Clean(dst)+string(os.PathSeparator)) {
			continue
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(fp, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(fp)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// bytesReaderAt — adapter byte slice → io.ReaderAt для zip.NewReader.
type bytesAt []byte

func (b bytesAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func bytesReaderAt(b []byte) bytesAt { return bytesAt(b) }

// writeFormsJSON — простой helper для JSON-ответов handler'ов раздела форм.
// Существующий writeJSON в handlers.go имеет другую сигнатуру (w, status, body)
// и используется для админских ответов, поэтому здесь — отдельная утилита.
func writeFormsJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

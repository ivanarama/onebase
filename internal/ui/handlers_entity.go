package ui

// HTTP-обработчики CRUD сущностей (справочники/документы): списки,
// формы, сохранение, проведение, удаление, табличные части, нумерация.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
)

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	params := parseListParams(r, entity, s.store.GetListPageSize(r.Context()))

	view := r.URL.Query().Get("view")
	treeView := entity.Hierarchical && view == "tree"
	tilesView := view == "tiles"
	feed := !treeView && s.resolveListMode(w, r, entity)

	var breadcrumbs []map[string]string
	var parentStr string
	var upURL string
	if entity.Hierarchical && !treeView {
		parentStr = r.URL.Query().Get("parent")
		if parentStr == "" {
			params.ParentStr = "root"
		} else {
			params.ParentStr = parentStr
			breadcrumbs = s.buildHierarchyBreadcrumbs(r.Context(), entity, parentStr)
			baseListURL := "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + strings.ToLower(entity.Name)
			csys := r.URL.Query().Get("subsystem")
			if len(breadcrumbs) <= 1 {
				if csys != "" {
					upURL = baseListURL + "?subsystem=" + csys
				} else {
					upURL = baseListURL
				}
			} else {
				pid := breadcrumbs[len(breadcrumbs)-2]["ID"]
				if csys != "" {
					upURL = baseListURL + "?parent=" + pid + "&subsystem=" + csys
				} else {
					upURL = baseListURL + "?parent=" + pid
				}
			}
		}
	}

	total, _ := s.store.CountList(r.Context(), entity.Name, entity, params)

	rows, err := s.store.List(r.Context(), entity.Name, entity, params)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	s.resolveRefs(r.Context(), entity, rows)
	markActivityRows(entity, rows)

	// For tree view: fetch ALL items and build hierarchical order with depth info
	var treeRows []map[string]any
	if treeView {
		allRows, _ := s.store.List(r.Context(), entity.Name, entity, storage.ListParams{ActivityScope: metadata.ActivityScopeAll})
		s.resolveRefs(r.Context(), entity, allRows)
		markActivityRows(entity, allRows)
		treeRows = buildCatalogTree(allRows)
	}

	refFilterOptions, _ := s.loadRefFilterOptions(r.Context(), entity)

	user := auth.UserFromContext(r.Context())
	isAdmin := user == nil || user.IsAdmin

	// Pagination info
	page := 1
	if params.Offset > 0 && params.Limit > 0 {
		page = params.Offset/params.Limit + 1
	}
	totalPages := 1
	if params.Limit > 0 && total > 0 {
		totalPages = (total + params.Limit - 1) / params.Limit
	}

	lang := s.resolveLang(r)
	s.render(w, r, "page-list", map[string]any{
		"Entity":           entity,
		"Rows":             rows,
		"Params":           params,
		"RefFilterOptions": refFilterOptions,
		"IsAdmin":          isAdmin,
		"Breadcrumbs":      breadcrumbs,
		"ParentStr":        parentStr,
		"UpURL":            upURL,
		"TreeView":         treeView,
		"TilesView":        tilesView,
		"Feed":             feed,
		"TreeRows":         treeRows,
		"Total":            total,
		"Page":             page,
		"TotalPages":       totalPages,
		"HasPrev":          page > 1,
		"HasNext":          page < totalPages,
		"PrevPage":         page - 1,
		"NextPage":         page + 1,
		"EnumLabels":       s.buildEnumLabels(entity, lang),
	})
}

// buildCatalogTree converts a flat list of catalog rows into a depth-first ordered
// list, adding "_depth" (int) and "_label" to each row for tree rendering.
func buildCatalogTree(rows []map[string]any) []map[string]any {
	children := make(map[string][]map[string]any)
	for _, row := range rows {
		pid := ""
		if v := row["parent_id"]; v != nil {
			pid = fmt.Sprintf("%v", v)
		}
		children[pid] = append(children[pid], row)
	}

	var result []map[string]any
	var walk func(pid string, depth int)
	walk = func(pid string, depth int) {
		for _, row := range children[pid] {
			row["_depth"] = depth
			result = append(result, row)
			id := fmt.Sprintf("%v", row["id"])
			walk(id, depth+1)
		}
	}
	walk("", 0)
	return result
}

func markActivityRows(entity *metadata.Entity, rows []map[string]any) {
	if entity == nil || entity.Activity == nil {
		return
	}
	for _, row := range rows {
		row["_activity_inactive"] = explicitFalse(row[entity.Activity.Field])
	}
}

func explicitFalse(v any) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return !t
	case int:
		return t == 0
	case int64:
		return t == 0
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		return s == "false" || s == "0" || s == "нет"
	}
	return false
}

// buildEnumLabels строит карту имя_поля → значение → перевод(lang) для
// enum-полей сущности — для отображения переведённых значений в списках.
// Исходное значение в данных строки не подменяется (остаётся идентификатором).
func (s *Server) buildEnumLabels(entity *metadata.Entity, lang string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, f := range entity.Fields {
		if f.EnumName == "" {
			continue
		}
		en := s.reg.GetEnum(f.EnumName)
		if en == nil {
			continue
		}
		out[f.Name] = enumValueLabels(en, lang)
	}
	return out
}

// buildTPEnumLabels строит карту tpName → fieldName → value → перевод(lang)
// для enum-полей табличных частей сущности — для передачи в SlickGrid.
func (s *Server) buildTPEnumLabels(entity *metadata.Entity, lang string) map[string]map[string]map[string]string {
	out := map[string]map[string]map[string]string{}
	for _, tp := range entity.TableParts {
		fieldMap := map[string]map[string]string{}
		for _, f := range tp.Fields {
			if f.EnumName == "" {
				continue
			}
			en := s.reg.GetEnum(f.EnumName)
			if en == nil {
				continue
			}
			fieldMap[f.Name] = enumValueLabels(en, lang)
		}
		if len(fieldMap) > 0 {
			out[tp.Name] = fieldMap
		}
	}
	return out
}

// buildTPEnumOrder строит карту tpName → fieldName → []value в порядке объявления
// values перечисления — для DOM-ТЧ applyTableParts, чтобы <option> шли в правильном
// семантическом порядке, а не в алфавитном (Object.keys не гарантирует порядок).
func (s *Server) buildTPEnumOrder(entity *metadata.Entity) map[string]map[string][]string {
	out := map[string]map[string][]string{}
	for _, tp := range entity.TableParts {
		fieldOrder := map[string][]string{}
		for _, f := range tp.Fields {
			if f.EnumName == "" {
				continue
			}
			en := s.reg.GetEnum(f.EnumName)
			if en == nil {
				continue
			}
			fieldOrder[f.Name] = en.Values
		}
		if len(fieldOrder) > 0 {
			out[tp.Name] = fieldOrder
		}
	}
	return out
}

func (s *Server) form(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "write") {
		return
	}
	refOptions, _ := s.loadRefOptions(r.Context(), entity)
	tpRefOpts, _ := s.loadTPRefOptions(r.Context(), entity)
	lang := s.resolveLang(r)
	enumOpts := s.loadEnumOptions(entity, lang)
	tpEnumLabels := s.buildTPEnumLabels(entity, lang)
	// Pre-fill date fields with current datetime for new documents
	values := map[string]string{}
	if entity.Kind == metadata.KindDocument {
		now := time.Now().Format("2006-01-02T15:04")
		for _, f := range entity.Fields {
			if f.Type == metadata.FieldTypeDate {
				values[f.Name] = now
			}
		}
	}
	if entity.Activity != nil {
		values[entity.Activity.Field] = "true"
	}
	tablePartRows := map[string][]map[string]any{}
	var fillError string
	var fillMessages []string

	// Ввод на основании: GET /ui/{kind}/{name}/new?based_on=<src>&based_on_id=<uuid>.
	// Загружаем источник и запускаем ОбработкаЗаполнения у приёмника — её
	// результаты (Fields + TablePartRows) перетирают дефолтные значения.
	if srcType := r.URL.Query().Get("based_on"); srcType != "" {
		fillError = s.applyFillFromQuery(r, entity, srcType, r.URL.Query().Get("based_on_id"), values, tablePartRows, &fillMessages)
	}
	var folderOpts []map[string]any
	if entity.Hierarchical {
		folderOpts = s.loadFolderOptions(r.Context(), entity)
		values["parent_id"] = r.URL.Query().Get("parent")
		if r.URL.Query().Get("is_folder") == "true" {
			values["is_folder"] = "true"
		} else {
			values["is_folder"] = "false"
		}
	}
	s.renderEntityForm(w, r, "object", map[string]any{
		"Entity":        entity,
		"IsNew":         true,
		"Values":        values,
		"RefOptions":    refOptions,
		"EnumOptions":   enumOpts,
		"TPRefOptions":  tpRefOpts,
		"TPEnumLabels":  tpEnumLabels,
		"TPEnumOrder":   s.buildTPEnumOrder(entity),
		"TPRefMeta":     tpRefMeta(entity),
		"TablePartRows": tablePartRows,
		"FolderOptions": folderOpts,
		"Error":         fillError,
		"Messages":      fillMessages,
		// IsPopup — форма открыта в iframe для inline-создания из другой
		// формы (как «новый элемент справочника» из поля документа в 1С).
		// Шаблон скрывает nav/тулбар и меняет кнопку на «Записать и выбрать».
		"IsPopup": r.URL.Query().Get("_popup") == "1",
	})
}

// applyFillFromQuery загружает источник и запускает ОбработкаЗаполнения у
// приёмника, втягивает результаты в values + tablePartRows (модифицируются
// in-place). Возвращает строку для шаблона "Error" — пустая, если всё ок.
//
// Ошибки доступа/валидации (нет прав на источник, srcType не в based_on)
// возвращаются как fillError-строки, форма всё равно открывается (пустая
// или с уже накопленными дефолтами). Так пользователь видит причину
// провала, но может продолжить ввод вручную.
func (s *Server) applyFillFromQuery(r *http.Request, entity *metadata.Entity, srcType, srcIDStr string, values map[string]string, tablePartRows map[string][]map[string]any, messages *[]string) string {
	srcID, err := uuid.Parse(srcIDStr)
	if err != nil {
		return "Некорректный идентификатор основания: " + srcIDStr
	}
	src := s.reg.GetEntity(srcType)
	if src == nil {
		return "Неизвестный тип основания: " + srcType
	}
	if !s.can(r, string(src.Kind), src.Name, "read") {
		return "Нет прав на чтение источника: " + srcType
	}
	result, err := s.entitySvc.Fill(r.Context(), entityservice.FillRequest{
		Receiver:   entity,
		SourceType: srcType,
		SourceID:   srcID,
	})
	if err != nil {
		return err.Error()
	}
	for k, v := range result.Fields {
		if v == nil {
			continue
		}
		values[fieldKeyForForm(entity, k)] = formatFieldValueForInput(v)
	}
	for tpName, rows := range result.TablePartRows {
		if rows != nil {
			tablePartRows[tpName] = rows
		}
	}
	if messages != nil && len(result.DSLMessages) > 0 {
		*messages = append(*messages, result.DSLMessages...)
	}
	return result.DSLError
}

// fieldKeyForForm возвращает имя поля в том регистре, в котором его ждёт
// шаблон (PascalCase из YAML). Object.Set/Fields у нас хранятся в lowercase
// — без приведения значения в форму не попадают (input name="Покупатель",
// values["покупатель"] не найдётся).
func fieldKeyForForm(entity *metadata.Entity, lowerKey string) string {
	for _, f := range entity.Fields {
		if strings.EqualFold(f.Name, lowerKey) {
			return f.Name
		}
	}
	return lowerKey
}

// formatFieldValueForInput приводит значение поля к строке для <input value=...>.
// Для *interpreter.Ref (после enrichHeaderRefs) — UUID, для time — RFC3339-короткий,
// иначе — fmt.Sprint.
func formatFieldValueForInput(v any) string {
	if v == nil {
		return ""
	}
	if t, ok := v.(time.Time); ok {
		return t.In(time.Local).Format("2006-01-02T15:04")
	}
	if ref, ok := v.(interface{ GetRefUUID() string }); ok {
		if s := ref.GetRefUUID(); s != "" {
			return s
		}
	}
	return fmt.Sprintf("%v", v)
}

// parseSubmitForm — общая часть submit и submitEdit. Парсит форму, строит
// объект, проверяет разрешения. Не строит коллектор движений и не вызывает
// enrich/DSL-хук — этим занимается entityservice.Service.Save (вызывается
// caller'ом ниже). Так избегается двойная работа (mc + enrich в двух местах).
//
// Если existingID == nil — создание нового объекта: id берётся из uuid.New,
// для документов с полем "Номер" автогенерируется номер. Если existingID != nil —
// редактирование: id берётся из URL, авто-нумерация не выполняется.
//
// Возвращает (nil,...,false) если запрос отклонён (нет прав / ошибка парсинга);
// в этом случае ответ уже записан в w.
func (s *Server) parseSubmitForm(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, existingID *uuid.UUID) (
	obj *runtime.Object, fields map[string]any, tpRows map[string][]map[string]any, action string, ok bool,
) {
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "write") {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), 400)
		return
	}
	// Лимит richtext проверяем по СЫРОМУ значению формы (до санитайза в
	// formToFields) — чтобы не прогонять гигантский blob через санитайзер.
	if err := checkRichTextLimits(r, entity); err != nil {
		http.Error(w, s.errText(r, err), 400)
		return
	}
	fields = formToFields(r, entity)
	tpRows = parseTablePartRows(r, entity)

	if entity.Hierarchical {
		fields["parent_id"] = r.FormValue("parent_id")
		fields["is_folder"] = r.FormValue("is_folder") == "true"
	}

	// Объект для new строится через NewObject+Set (ключи нормализуются в lowercase
	// — историческое поведение submit). Для existing — прямое присваивание Fields,
	// ключи остаются как пришли из формы (PascalCase). fieldValueDialect в storage
	// читает значение по обоим вариантам ключа, так что save работает одинаково.
	if existingID == nil {
		obj = runtime.NewObject(entity.Name, entity.Kind)
		for k, v := range fields {
			obj.Set(k, v)
		}
		obj.TablePartRows = tpRows

		// Auto-number: fill Номер if empty for new documents
		if entity.Kind == metadata.KindDocument {
			for _, f := range entity.Fields {
				if f.Name == "Номер" && f.Type == metadata.FieldTypeString {
					if v := fmt.Sprintf("%v", obj.Fields["Номер"]); v == "" || v == "<nil>" {
						obj.Set("Номер", s.generateNumber(r.Context(), entity, obj.Fields))
					}
					break
				}
			}
		}
	} else {
		obj = &runtime.Object{
			Type:          entity.Name,
			Kind:          entity.Kind,
			ID:            *existingID,
			Fields:        fields,
			TablePartRows: tpRows,
		}
	}

	action = r.FormValue("_action")
	isPostingAct := entity.Posting && (action == "post" || action == "post_and_close")
	if isPostingAct && !s.requirePerm(w, r, string(entity.Kind), entity.Name, "post") {
		return
	}
	ok = true
	return
}

// renderObjectFormError перерисовывает форму объекта с баннером ошибки — когда
// серверный хук формы (ПередЗаписью/ПриЗаписи) отклонил запись через
// ВызватьИсключение. Контекст совпадает с веткой DSLError в submit/submitEdit.
func (s *Server) renderObjectFormError(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, isNew bool, errMsg string, msgs []string, tpRows map[string][]map[string]any) {
	refOptions, _ := s.loadRefOptions(r.Context(), entity)
	tpRefOpts, _ := s.loadTPRefOptions(r.Context(), entity)
	lang := s.resolveLang(r)
	data := map[string]any{
		"Entity":        entity,
		"IsNew":         isNew,
		"Error":         errMsg,
		"Messages":      msgs,
		"Values":        formValues(r, entity),
		"RefOptions":    refOptions,
		"EnumOptions":   s.loadEnumOptions(entity, lang),
		"TPRefOptions":  tpRefOpts,
		"TPEnumLabels":  s.buildTPEnumLabels(entity, lang),
		"TPEnumOrder":   s.buildTPEnumOrder(entity),
		"TPRefMeta":     tpRefMeta(entity),
		"TablePartRows": serializeTablePartRowsForEntity(tpRows, entity),
	}
	if isNew {
		if entity.Hierarchical {
			data["FolderOptions"] = s.loadFolderOptions(r.Context(), entity)
		}
		data["IsPopup"] = r.FormValue("_popup") == "1"
	}
	s.renderEntityForm(w, r, "object", data)
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	obj, fields, tpRows, action, ok := s.parseSubmitForm(w, r, entity, nil)
	if !ok {
		return
	}

	// Серверные события записи формы (ПередЗаписью/ПриЗаписи) — до Save. Бросок
	// ВызватьИсключение в обработчике отменяет запись и перерисовывает форму.
	var hookMsgs []string
	if hookErr := s.runPreSaveFormHooks(r.Context(), entity, obj, &hookMsgs); hookErr != nil {
		s.renderObjectFormError(w, r, entity, true, hookErr.Error(), hookMsgs, obj.TablePartRows)
		return
	}

	result, err := s.entitySvc.Save(r.Context(), entityservice.SaveRequest{
		Entity:        entity,
		ID:            obj.ID,
		IsNew:         true,
		Fields:        obj.Fields,
		TablePartRows: obj.TablePartRows,
		Action:        action,
	})
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	if result.DSLError != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		tpRefOpts, _ := s.loadTPRefOptions(r.Context(), entity)
		langErr := s.resolveLang(r)
		var fOpts []map[string]any
		if entity.Hierarchical {
			fOpts = s.loadFolderOptions(r.Context(), entity)
		}
		s.renderEntityForm(w, r, "object", map[string]any{
			"Entity":       entity,
			"IsNew":        true,
			"Error":        result.DSLError,
			"Messages":     result.DSLMessages,
			"Values":       formValues(r, entity),
			"RefOptions":   refOptions,
			"EnumOptions":  s.loadEnumOptions(entity, langErr),
			"TPRefOptions": tpRefOpts,
			"TPEnumLabels": s.buildTPEnumLabels(entity, langErr),
			"TPEnumOrder":  s.buildTPEnumOrder(entity),
			"TPRefMeta":    tpRefMeta(entity),
			// tpRows мог быть обогащён до *interpreter.Ref хуком проведения
			// (EnrichTPRows мутирует слайсы on place). jsJSON сериализовал бы
			// Ref как объект {UUID,Name,…} → грид показал бы «[object Object]».
			// Нормализуем обратно к UUID-строкам, как в обработчике form-event.
			"TablePartRows": serializeTablePartRowsForEntity(tpRows, entity),
			"FolderOptions": fOpts,
			"IsPopup":       r.FormValue("_popup") == "1",
		})
		return
	}

	// ПослеЗаписи — после успешной записи (Объект перезагружается с номером/ссылками).
	s.runAfterWriteFormHook(r.Context(), entity, obj.ID, &hookMsgs)

	// Popup-режим (создание из iframe в родительской форме): не редиректим,
	// а отдаём страничку, которая через postMessage сообщает родителю id
	// и подпись только что созданного объекта, после чего модалка закрывается.
	//
	// Важно: используем локальную fields (ключи в оригинальном регистре
	// после formToFields), а не obj.Fields — Object.Set приводит ключи к
	// нижнему регистру, и firstStringField не находит "Наименование".
	if r.FormValue("_popup") == "1" {
		s.renderPopupSaved(w, obj.ID.String(), firstStringField(fields, entity))
		return
	}

	if action == "post_and_close" {
		http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
		return
	}
	// "post" / "Записать" — остаёмся на форме
	http.Redirect(w, r, "/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/"+obj.ID.String(), http.StatusSeeOther)
}

// refCreateRedirect — точка входа для JS-кнопки «+ Создать» рядом с
// ссылочным полем. Клиент не знает kind целевой сущности (catalog/document)
// — резолвим по имени и редиректим на /ui/<kind>/<name>/new?_popup=1.
func (s *Server) refCreateRedirect(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "entity")
	ent := s.reg.GetEntity(name)
	if ent == nil {
		http.Error(w, s.tr(s.resolveLang(r), "Сущность не найдена")+": "+name, http.StatusNotFound)
		return
	}
	kind := strings.ToLower(string(ent.Kind))
	http.Redirect(w, r, "/ui/"+kind+"/"+ent.Name+"/new?_popup=1", http.StatusFound)
}

// refOpenRedirect — точка входа для иконки-лупы в picker'е («провалиться»
// в карточку выбранного элемента). JS знает имя сущности и id, kind
// резолвим по имени и редиректим на форму редактирования.
func (s *Server) refOpenRedirect(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "entity")
	id := chi.URLParam(r, "id")
	ent := s.reg.GetEntity(name)
	if ent == nil {
		http.Error(w, s.tr(s.resolveLang(r), "Сущность не найдена")+": "+name, http.StatusNotFound)
		return
	}
	kind := strings.ToLower(string(ent.Kind))
	http.Redirect(w, r, "/ui/"+kind+"/"+ent.Name+"/"+id, http.StatusFound)
}

// renderPopupSaved отдаёт минимальную HTML-страницу, которая через
// postMessage передаёт родительскому окну id и подпись созданного объекта.
// Родитель (см. openRefCreate в шаблоне) подставит значение в свой select
// и закроет модалку. Подпись экранируется через encoding/json — то же
// делает шаблон, но здесь без шаблона короче.
func (s *Server) renderPopupSaved(w http.ResponseWriter, id, label string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	idJSON, _ := json.Marshal(id)
	labelJSON, _ := json.Marshal(label)
	fmt.Fprintf(w, `<!doctype html><html><body><script>
try {
  window.parent.postMessage({source:"obRefCreate", id:%s, label:%s}, "*");
} catch (e) {}
</script>Готово.</body></html>`, idJSON, labelJSON)
}

// pickObjectFormWithReadHook возвращает форму ОБЪЕКТА (Kind=="object"),
// объявляющую серверный обработчик ПриЧтенииНаСервере, — независимо от того,
// managed она (forms/<entity>/*.form.yaml) или autogen (src/<entity>.form.os).
//
// Замечание #13: раньше RLS-хук на чтение искался только среди managed-форм
// (pickManagedForm), поэтому ПриЧтенииНаСервере, объявленный в обычной
// autogen-форме, молча игнорировался — footgun для RLS. runFormReadHook не
// требует managed-формы, ему нужны лишь Handlers + ProgramAST, которые есть и у
// autogen-формы (loader парсит .form.os в FormModule с Handlers/ProgramAST).
//
// Порядок выбора: сначала managed-форма объекта (она же используется при рендере,
// renderEntityForm отдаёт managed при наличии), затем autogen — чтобы хук считался
// с той же формы, которую увидит пользователь. Возвращаем только форму, реально
// объявляющую обработчик; если ни одна не объявляет — nil (хук не запускается).
func pickObjectFormWithReadHook(entity *metadata.Entity) *metadata.FormModule {
	return pickObjectFormWithHook(entity, string(metadata.FormEventOnReadAtServer))
}

// pickObjectFormWithHook возвращает форму ОБЪЕКТА (Kind=="object"), объявляющую
// серверный обработчик события evt. Приоритет — managed-форма (она же рендерится),
// затем autogen (src/<entity>.form.os): обработчик считается с той же формы,
// которую видит пользователь. Если ни одна не объявляет — nil (хук не запускается).
// Обобщение pickObjectFormWithReadHook на любое серверное событие формы
// (ПриЧтенииНаСервере, ПередЗаписью, ПриЗаписи, ПослеЗаписи).
func pickObjectFormWithHook(entity *metadata.Entity, evt string) *metadata.FormModule {
	if entity == nil {
		return nil
	}
	declares := func(fm *metadata.FormModule) bool {
		return fm != nil && strings.EqualFold(fm.Kind, "object") &&
			resolveHandlerProc(fm, "", evt) != ""
	}
	// 1) managed-форма объекта (приоритет — она же рендерится).
	for _, fm := range entity.Forms {
		if fm != nil && fm.IsManaged() && declares(fm) {
			return fm
		}
	}
	// 2) autogen-форма объекта (src/<entity>.form.os).
	for _, fm := range entity.Forms {
		if fm != nil && !fm.IsManaged() && declares(fm) {
			return fm
		}
	}
	return nil
}

func (s *Server) formEdit(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil {
		http.Error(w, s.errText(r, err), 404)
		return
	}
	// Issue #148: серверный обработчик ПриЧтенииНаСервере исполняется ДО рендера
	// HTML. Если он бросает исключение — отдаём 403 и не раскрываем данные записи
	// (row-level security на чтение). Без этого ПриОткрытии срабатывал лишь на
	// клиенте, уже после отдачи формы со всеми полями.
	//
	// Замечание #13: хук выполняется для формы объекта НЕЗАВИСИМО от её типа —
	// managed (forms/<entity>/*.form.yaml) ИЛИ autogen (src/<entity>.form.os).
	// runFormReadHook не требует managed-формы (нужны лишь Handlers + ProgramAST),
	// поэтому RLS на чтение, объявленный в autogen-форме, тоже срабатывает.
	if objForm := pickObjectFormWithReadHook(entity); objForm != nil {
		if denied := s.runFormReadHook(r.Context(), entity, objForm, id); denied != nil {
			s.renderForbidden(w, r)
			return
		}
	}
	refOptions, _ := s.loadRefOptions(r.Context(), entity)
	tpRefOpts, _ := s.loadTPRefOptions(r.Context(), entity)
	langEdit := s.resolveLang(r)
	enumOpts := s.loadEnumOptions(entity, langEdit)
	tpEnumLabelsEdit := s.buildTPEnumLabels(entity, langEdit)
	vals := make(map[string]string)
	for _, f := range entity.Fields {
		v := row[f.Name]
		if v == nil {
			continue
		}
		if f.Type == metadata.FieldTypeDate {
			if t, ok := v.(time.Time); ok {
				vals[f.Name] = t.In(time.Local).Format("2006-01-02T15:04")
				continue
			}
			// SQLite returns dates as strings — parse and reformat for <input type="datetime-local">
			if s2, ok := v.(string); ok && s2 != "" {
				parsed := false
				for _, layout := range []string{
					time.RFC3339, time.RFC3339Nano,
					"2006-01-02 15:04:05 -0700 MST",
					"2006-01-02 15:04:05.999999999 -0700 MST",
					"2006-01-02T15:04:05", "2006-01-02 15:04:05",
					"2006-01-02T15:04", "2006-01-02",
				} {
					if t, err2 := time.Parse(layout, s2); err2 == nil {
						vals[f.Name] = t.In(time.Local).Format("2006-01-02T15:04")
						parsed = true
						break
					}
				}
				// Last resort: extract just the date prefix
				if !parsed && len(s2) >= 10 {
					if t, err2 := time.ParseInLocation("2006-01-02", s2[:10], time.Local); err2 == nil {
						vals[f.Name] = t.Format("2006-01-02T15:04")
					}
				}
				continue
			}
		}
		if f.Type == metadata.FieldTypeBool {
			if asBool(v) {
				vals[f.Name] = "true"
			} else {
				vals[f.Name] = "false"
			}
			continue
		}
		vals[f.Name] = fmt.Sprintf("%v", v)
	}
	if entity.Activity != nil {
		if vals[entity.Activity.Field] == "" {
			vals[entity.Activity.Field] = "true"
		}
	}
	// Include posted status + deletion mark for documents
	if entity.Kind == metadata.KindDocument {
		vals["posted"] = fmt.Sprintf("%v", row["posted"])
		// deletion_mark нормализуем к каноничным "true"/"false": GetByID гонит его
		// через normalizeValue, и на SQLite помеченный документ приходит как
		// int64(1) (а не bool). Шаблон формы сравнивает с литералом "true"
		// (скрыть «Провести», показать «Снять пометку»), поэтому сырое "1"
		// ломало бы UI на SQLite. asBool понимает bool/int/int64 одинаково.
		if asBool(row["deletion_mark"]) {
			vals["deletion_mark"] = "true"
		} else {
			vals["deletion_mark"] = "false"
		}
	}
	// _version нужен на форме как hidden — для оптимистической блокировки
	// при последующем POST'е в submitEdit. См. storage.UpsertVersioned.
	if v, ok := row["_version"]; ok && v != nil {
		vals["_version"] = fmt.Sprintf("%v", v)
	}

	tpRows := make(map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		rows, err := s.store.GetTablePartRows(r.Context(), entity.Name, tp.Name, id, tp)
		if err == nil {
			tpRows[tp.Name] = rows
		}
	}

	var folderOptsEdit []map[string]any
	if entity.Hierarchical {
		folderOptsEdit = s.loadFolderOptions(r.Context(), entity)
		if v, ok := row["is_folder"]; ok {
			if asBool(v) {
				vals["is_folder"] = "true"
			} else {
				vals["is_folder"] = "false"
			}
		} else {
			vals["is_folder"] = "false"
		}
		if v, ok := row["parent_id"]; ok && v != nil {
			vals["parent_id"] = fmt.Sprintf("%v", v)
		}
	}

	editUser := auth.UserFromContext(r.Context())
	editIsAdmin := editUser == nil || editUser.IsAdmin

	// Load document movements for posted documents
	var docMovements map[string][]map[string]any
	if entity.Kind == metadata.KindDocument && vals["posted"] == "true" {
		docMovements, _ = s.store.GetDocumentMovements(r.Context(), id, s.reg.Registers())
		for regName, regRows := range docMovements {
			if reg := s.reg.GetRegister(regName); reg != nil {
				s.resolveRegisterRows(r.Context(), regRows, reg)
			}
		}
	}

	s.renderEntityForm(w, r, "object", map[string]any{
		"Entity":        entity,
		"IsNew":         false,
		"Values":        vals,
		"RefOptions":    refOptions,
		"EnumOptions":   enumOpts,
		"TPRefOptions":  tpRefOpts,
		"TPEnumLabels":  tpEnumLabelsEdit,
		"TPEnumOrder":   s.buildTPEnumOrder(entity),
		"TPRefMeta":     tpRefMeta(entity),
		"TablePartRows": tpRows,
		"ID":            id.String(),
		"IsAdmin":       editIsAdmin,
		"PrintForms":    s.reg.GetPrintForms(entity.Name),
		"DSLPrintForms": s.reg.GetDSLPrintForms(entity.Name),
		// AllPrintForms — единый список форм всех видов (план 64, этап 3);
		// кнопка «Печать ▾» рисуется одним циклом по нему.
		"AllPrintForms": s.reg.GetAllPrintForms(entity.Name),
		"HasPrintProc":  s.reg.GetProcedure(entity.Name, "Печать") != nil || s.reg.GetProcedure(entity.Name, "Print") != nil,
		"FolderOptions": folderOptsEdit,
		"DocMovements":  docMovements,
		"Error":         buildEditError(r),
		// Receivers — список сущностей, у которых в based_on указан текущий
		// объект. Шаблон рисует выпадающую кнопку «Ввести на основании ▾» —
		// аналог одноимённой команды в 1С:Предприятие.
		"Receivers": s.reg.ReceiversOf(entity.Name),
	})
}

// buildEditError собирает сообщение об ошибке из query-параметров, пришедших
// после redirect'а. posting_error — сбой ОбработкиПроведения; conflict=1 —
// оптимистический конфликт версий (см. storage.ErrVersionConflict).
func buildEditError(r *http.Request) string {
	if r.URL.Query().Get("conflict") == "1" {
		return "Объект был изменён другим пользователем, пока вы редактировали форму. Ваши изменения не сохранены — текущие данные перезагружены."
	}
	return r.URL.Query().Get("posting_error")
}

// renderVersionConflict перезагружает форму редактирования с актуальными
// данными из БД и показывает пользователю сообщение об оптимистическом
// конфликте. Изменения пользователя теряются — это сознательный выбор
// (lost-update лучше тихого перетирания чужих правок).
func (s *Server) renderVersionConflict(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, id uuid.UUID) {
	target := "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + id.String() + "?conflict=1"
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) submitEdit(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	obj, _, tpRows, action, ok := s.parseSubmitForm(w, r, entity, &id)
	if !ok {
		return
	}

	// Серверные события записи формы (ПередЗаписью/ПриЗаписи) — до Save. Бросок
	// ВызватьИсключение в обработчике отменяет запись и перерисовывает форму.
	var hookMsgs []string
	if hookErr := s.runPreSaveFormHooks(r.Context(), entity, obj, &hookMsgs); hookErr != nil {
		s.renderObjectFormError(w, r, entity, false, hookErr.Error(), hookMsgs, obj.TablePartRows)
		return
	}

	// Парсим _version для оптимистической блокировки. Если поля нет —
	// expectedVersion=nil, UpsertVersioned не проверяет (поведение как раньше).
	var expectedVersion *int64
	if vStr := r.FormValue("_version"); vStr != "" {
		if v, perr := strconv.ParseInt(vStr, 10, 64); perr == nil {
			expectedVersion = &v
		}
	}

	result, err := s.entitySvc.Save(r.Context(), entityservice.SaveRequest{
		Entity:          entity,
		ID:              obj.ID,
		IsNew:           false,
		Fields:          obj.Fields,
		TablePartRows:   obj.TablePartRows,
		Action:          action,
		ExpectedVersion: expectedVersion,
	})
	if err != nil {
		// Оптимистический конфликт: перечитываем актуальное состояние из БД
		// и показываем пользователю с понятным сообщением. Свои изменения
		// он потеряет — но это лучше, чем тихо перетереть чужие.
		if errors.Is(err, storage.ErrVersionConflict) {
			s.renderVersionConflict(w, r, entity, id)
			return
		}
		http.Error(w, s.errText(r, err), 500)
		return
	}
	if result.DSLError != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		tpRefOpts2, _ := s.loadTPRefOptions(r.Context(), entity)
		langSubmit := s.resolveLang(r)
		s.renderEntityForm(w, r, "object", map[string]any{
			"Entity":       entity,
			"IsNew":        false,
			"Error":        result.DSLError,
			"Values":       formValues(r, entity),
			"RefOptions":   refOptions,
			"EnumOptions":  s.loadEnumOptions(entity, langSubmit),
			"TPRefOptions": tpRefOpts2,
			"TPEnumLabels": s.buildTPEnumLabels(entity, langSubmit),
			"TPEnumOrder":  s.buildTPEnumOrder(entity),
			"TPRefMeta":    tpRefMeta(entity),
			// См. submit: проведение могло обогатить tpRows до *Ref —
			// сериализуем к UUID-строкам, иначе грид покажет «[object Object]».
			"TablePartRows": serializeTablePartRowsForEntity(tpRows, entity),
		})
		return
	}

	// ПослеЗаписи — после успешной записи (Объект перезагружается с номером/ссылками).
	s.runAfterWriteFormHook(r.Context(), entity, id, &hookMsgs)

	if action == "post_and_close" {
		http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
		return
	}
	// "Записать" — остаёмся на форме
	http.Redirect(w, r, "/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/"+id.String(), http.StatusSeeOther)
}

// postDocument posts a document: runs ОбработкаПроведения, writes movements, sets posted=true.
func (s *Server) postDocument(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "post") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil {
		http.Error(w, s.errText(r, err), 404)
		return
	}

	if asBool(row["deletion_mark"]) {
		// Помеченный на удаление документ проводить нельзя.
		http.Redirect(w, r,
			"/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/"+id.String()+
				"?posting_error="+url.QueryEscape("Документ помечен на удаление: проведение невозможно"),
			http.StatusSeeOther)
		return
	}

	obj := &runtime.Object{ID: id, Type: entity.Name, Kind: entity.Kind, Fields: make(map[string]any)}
	for _, f := range entity.Fields {
		obj.Fields[f.Name] = row[f.Name]
	}
	tpRows := make(map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		rows, _ := s.store.GetTablePartRows(r.Context(), entity.Name, tp.Name, id, tp)
		s.enrichTPRowsWithRefs(r.Context(), tp, rows)
		tpRows[tp.Name] = rows
	}
	obj.TablePartRows = tpRows

	mc := runtime.NewMovementsCollector(entity.Name, id)
	setPeriodFromFields(mc, entity, obj.Fields)

	docURL := "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + id.String()
	// Дата запрета проведения (свёртка базы, план 74).
	if mc.Period != nil {
		if lock, ok := s.store.GetPostingLockDate(r.Context()); ok && storage.PostingFrozen(lock, *mc.Period) {
			http.Redirect(w, r, docURL+"?posting_error="+url.QueryEscape(storage.PostingFrozenError(lock).Error()), http.StatusSeeOther)
			return
		}
	}
	if errMsg, _ := s.runOnPostCtx(r.Context(), obj, mc); errMsg != "" {
		http.Redirect(w, r, docURL+"?posting_error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if err := s.saveMovements(ctx, entity.Name, id, mc); err != nil {
			return err
		}
		return s.store.SetPosted(ctx, entity.Name, id, true)
	}); err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	http.Redirect(w, r, docURL, http.StatusSeeOther)
}

// clearMovements removes all register movements (accumulation, info, account)
// recorded by the given document, across every register. Passing nil rows to the
// register writers performs only the DELETE-by-recorder step, so iterating all
// registers is safe even for those the document never wrote to.
func (s *Server) clearMovements(ctx context.Context, entityName string, id uuid.UUID) error {
	for _, reg := range s.reg.Registers() {
		if err := s.store.WriteMovements(ctx, reg.Name, entityName, id, nil, reg, nil); err != nil {
			return err
		}
	}
	for _, ir := range s.reg.InfoRegisters() {
		if err := s.store.WriteInfoMovements(ctx, ir.Name, entityName, id, nil, ir, nil); err != nil {
			return err
		}
	}
	for _, ar := range s.reg.AccountRegisters() {
		if err := s.store.WriteAccountMovements(ctx, ar.Name, entityName, id, nil, ar, nil); err != nil {
			return err
		}
	}
	return nil
}

// markForDeletion помечает/снимает пометку на удаление. При пометке проведённого
// документа сперва отменяет проведение (чистит движения по всем регистрам и
// снимает posted) — пометка и проведённость взаимоисключающи (как в 1С). Снятие
// пометки проведение НЕ возвращает. Транзакцию метод не открывает: HTTP-вызовы
// оборачивают его в store.WithTx, DSL-путь использует живой ctx (как DeleteRef).
func (s *Server) markForDeletion(ctx context.Context, entity *metadata.Entity, id uuid.UUID, mark bool) error {
	if mark && entity.Posting {
		row, err := s.store.GetByID(ctx, entity.Name, id, entity)
		if err != nil {
			return err
		}
		if asBool(row["posted"]) {
			if err := s.clearMovements(ctx, entity.Name, id); err != nil {
				return err
			}
			if err := s.store.SetPosted(ctx, entity.Name, id, false); err != nil {
				return err
			}
		}
	}
	return s.store.MarkForDeletion(ctx, entity.Name, id, mark)
}

// unpostDocument clears movements and sets posted=false.
func (s *Server) unpostDocument(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "unpost") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if err := s.clearMovements(ctx, entity.Name, id); err != nil {
			return err
		}
		return s.store.SetPosted(ctx, entity.Name, id, false)
	}); err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	// Веб-хук document.unpost (план 29) — после успешной транзакции.
	if s.cfg.Webhooks.Enabled() {
		s.cfg.Webhooks.Dispatch(webhook.Event{
			Name: "document.unpost", Entity: entity.Name, ID: id.String(),
			User: storage.AuditUserLogin(r.Context()),
		})
	}
	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

func (s *Server) setRecordActivity(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if entity.Activity == nil {
		http.Error(w, "activity is not configured", http.StatusBadRequest)
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "write") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	active := r.URL.Query().Get("active") == "1" || strings.EqualFold(r.URL.Query().Get("active"), "true")
	if err := s.store.SetActivity(r.Context(), entity, id, active); err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	http.Redirect(w, r, safeBackURL(r, listURL(entity)), http.StatusSeeOther)
}

func safeBackURL(r *http.Request, fallback string) string {
	ref := strings.TrimSpace(r.Referer())
	if ref == "" {
		return fallback
	}
	u, err := url.Parse(ref)
	if err != nil {
		return fallback
	}
	if u.IsAbs() && u.Host != r.Host {
		return fallback
	}
	if !u.IsAbs() && !strings.HasPrefix(ref, "/") {
		return fallback
	}
	return ref
}

// deleteRecord: admin → permanent delete (with ref check); non-admin → mark for deletion.
func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "delete") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	user := auth.UserFromContext(r.Context())
	isAdmin := user == nil || user.IsAdmin // no auth configured → treat as admin
	markParam := r.URL.Query().Get("mark")

	// Снятие пометки на удаление (mark=0) — без возврата проведения.
	if markParam == "0" {
		if err := s.store.MarkForDeletion(r.Context(), entity.Name, id, false); err != nil {
			http.Error(w, s.errText(r, err), 500)
			return
		}
		http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
		return
	}

	if !isAdmin || markParam == "1" {
		// Non-admin или явная пометка: пометить на удаление с авто-отменой
		// проведения для проведённого документа (в одной транзакции).
		if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
			return s.markForDeletion(ctx, entity, id, true)
		}); err != nil {
			http.Error(w, s.errText(r, err), 500)
			return
		}
		http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
		return
	}

	// Admin: check references before permanent delete
	refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
	if len(refs) > 0 {
		var msg strings.Builder
		lang := s.resolveLang(r)
		msg.WriteString(s.tr(lang, "Невозможно удалить: объект используется в:") + "\n")
		recordsWord := s.tr(lang, "записей")
		for _, ref := range refs {
			fmt.Fprintf(&msg, "  • %s.%s (%d %s)\n", ref.EntityName, ref.FieldName, ref.Count, recordsWord)
		}
		http.Error(w, msg.String(), 409)
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if entity.Posting {
			if err := s.clearMovements(ctx, entity.Name, id); err != nil {
				return err
			}
		}
		return s.store.Delete(ctx, entity.Name, id)
	}); err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	// Веб-хук <kind>.delete (план 29) — только физическое удаление
	// (пометка на удаление обратима и событием не считается).
	if s.cfg.Webhooks.Enabled() {
		s.cfg.Webhooks.Dispatch(webhook.Event{
			Name: string(entity.Kind) + ".delete", Entity: entity.Name, ID: id.String(),
			User: storage.AuditUserLogin(r.Context()),
		})
	}
	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

// deleteMarkedAll is the global "Удалить помеченные" page accessible from the system menu.
// GET: shows all marked records across every entity.
// POST: deletes all marked records that have no references.
func (s *Server) deleteMarkedAll(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}

	type markedEntry struct {
		EntityName string
		Kind       string
		ID         string
		Label      string
		HasRefs    bool
	}

	if r.Method == http.MethodPost {
		deleted, skipped := 0, 0
		for _, entity := range s.reg.Entities() {
			marked, err := s.store.ListMarked(r.Context(), entity.Name, entity)
			if err != nil {
				continue
			}
			for _, row := range marked {
				idStr, _ := row["id"].(string)
				id, err := uuid.Parse(idStr)
				if err != nil {
					continue
				}
				refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
				if len(refs) > 0 {
					skipped++
					continue
				}
				if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
					if entity.Posting {
						if err := s.clearMovements(ctx, entity.Name, id); err != nil {
							return err
						}
					}
					for _, tp := range entity.TableParts {
						s.store.Exec(ctx, "DELETE FROM "+metadata.TablePartTableName(entity.Name, tp.Name)+" WHERE parent_id = "+s.store.Dialect().Placeholder(1), id)
					}
					return s.store.Delete(ctx, entity.Name, id)
				}); err != nil {
					// Удаление не прошло (откат транзакции) — не рапортуем успех.
					skipped++
					continue
				}
				deleted++
			}
		}
		http.Redirect(w, r,
			fmt.Sprintf("/ui/delete-marked?deleted=%d&skipped=%d", deleted, skipped),
			http.StatusSeeOther)
		return
	}

	// GET: collect all marked records
	var entries []markedEntry
	for _, entity := range s.reg.Entities() {
		rows, err := s.store.ListMarked(r.Context(), entity.Name, entity)
		if err != nil {
			continue
		}
		for _, row := range rows {
			idStr, _ := row["id"].(string)
			id, _ := uuid.Parse(idStr)
			refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
			entries = append(entries, markedEntry{
				EntityName: entity.Name,
				Kind:       string(entity.Kind),
				ID:         idStr,
				Label:      firstStringField(row, entity),
				HasRefs:    len(refs) > 0,
			})
		}
	}

	deleted, _ := strconv.Atoi(r.URL.Query().Get("deleted"))
	skipped, _ := strconv.Atoi(r.URL.Query().Get("skipped"))
	s.render(w, r, "page-delete-marked", map[string]any{
		"Entries": entries,
		"Deleted": deleted,
		"Skipped": skipped,
	})
}

// deleteMarked permanently deletes all deletion_mark=true records without references.
func (s *Server) deleteMarked(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}

	user := auth.UserFromContext(r.Context())
	if user != nil && !user.IsAdmin {
		s.renderForbidden(w, r)
		return
	}

	marked, err := s.store.ListMarked(r.Context(), entity.Name, entity)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}

	deleted, skipped := 0, 0
	for _, row := range marked {
		idStr, _ := row["id"].(string)
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
		if len(refs) > 0 {
			skipped++
			continue
		}
		if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
			if entity.Posting {
				if err := s.clearMovements(ctx, entity.Name, id); err != nil {
					return err
				}
			}
			return s.store.Delete(ctx, entity.Name, id)
		}); err != nil {
			// Удаление не прошло (откат транзакции) — не рапортуем успех.
			skipped++
			continue
		}
		deleted++
	}

	http.Redirect(w, r,
		fmt.Sprintf("%s?deleted=%d&skipped=%d", listURL(entity), deleted, skipped),
		http.StatusSeeOther)
}

func (s *Server) saveMovements(ctx context.Context, docType string, docID uuid.UUID, mc *runtime.MovementsCollector) error {
	for regName, rows := range mc.All() {
		// try accumulation register first
		reg := s.reg.GetRegister(regName)
		if reg != nil {
			if err := s.store.WriteMovements(ctx, regName, docType, docID, rows, reg, mc.Period); err != nil {
				return err
			}
			continue
		}
		// try account register
		ar := s.reg.GetAccountRegister(regName)
		if ar != nil {
			if err := s.store.WriteAccountMovements(ctx, regName, docType, docID, rows, ar, mc.Period); err != nil {
				return err
			}
			continue
		}
		// try info register (замечание #23)
		ir := s.reg.GetInfoRegister(regName)
		if ir != nil {
			if err := s.store.WriteInfoMovements(ctx, regName, docType, docID, rows, ir, mc.Period); err != nil {
				return err
			}
		}
	}
	return nil
}

// setPeriodFromFields sets the movements period from the first date field of the document.
func setPeriodFromFields(mc *runtime.MovementsCollector, entity *metadata.Entity, fields map[string]any) {
	for _, f := range entity.Fields {
		if f.Type != metadata.FieldTypeDate {
			continue
		}
		// Регистронезависимый поиск: ключи Fields бывают и в PascalCase
		// (formToFields / GetByID), и в lower-case (после Object.Set).
		// Прямой fields[f.Name] промахивался на пути submit → period = time.Now().
		low := strings.ToLower(f.Name)
		for k, v := range fields {
			if strings.ToLower(k) != low {
				continue
			}
			if t := runtime.AsTime(v); !t.IsZero() {
				mc.SetPeriod(t)
			}
			break
		}
		return
	}
}

// saveTablePartsDirect persists tablepart rows from the provided map (possibly modified by DSL).
func (s *Server) saveTablePartsDirect(ctx context.Context, entity *metadata.Entity, parentID uuid.UUID, tpRows map[string][]map[string]any) error {
	for _, tp := range entity.TableParts {
		rows := tpRows[tp.Name]
		if rows == nil {
			rows = []map[string]any{}
		}
		if err := s.store.UpsertTablePartRows(ctx, entity.Name, tp.Name, parentID, rows, tp); err != nil {
			return err
		}
	}
	return nil
}

// parseTablePartRows reads tp.{TpName}.{idx}.{FieldName} form values.
func parseTablePartRows(r *http.Request, entity *metadata.Entity) map[string][]map[string]any {
	result := make(map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		// Plan 48 (SlickGrid): check for tp_json.{TPName} first.
		if jsonBlob := r.FormValue("tp_json." + tp.Name); jsonBlob != "" {
			var rows []map[string]any
			if err := json.Unmarshal([]byte(jsonBlob), &rows); err == nil {
				// Normalize field names to original case + convert types.
				cleaned := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					if len(row) == 0 {
						continue
					}
					converted := make(map[string]any, len(row))
					for _, f := range tp.Fields {
						// Normalize case: try exact, then case-insensitive match.
						var raw string
						if v, ok := row[f.Name]; ok {
							raw = fmt.Sprintf("%v", v)
						} else {
							for k, v := range row {
								if strings.EqualFold(k, f.Name) {
									raw = fmt.Sprintf("%v", v)
									break
								}
							}
						}
						switch f.Type {
						case metadata.FieldTypeNumber:
							if n, err := strconv.ParseFloat(raw, 64); err == nil {
								converted[f.Name] = n
							} else {
								converted[f.Name] = raw
							}
						case metadata.FieldTypeBool:
							converted[f.Name] = raw == "true"
						default:
							converted[f.Name] = raw
						}
					}
					// Skip rows where all fields are blank (like legacy path).
					empty := true
					for _, f := range tp.Fields {
						if v, ok := converted[f.Name]; ok && fmt.Sprintf("%v", v) != "" {
							empty = false
							break
						}
					}
					if !empty {
						cleaned = append(cleaned, converted)
					}
				}
				result[tp.Name] = cleaned
				continue // skip legacy named-input parsing for this TP
			}
		}
		// collect max index
		maxIdx := -1
		prefix := "tp." + tp.Name + "."
		for key := range r.Form {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := strings.TrimPrefix(key, prefix)
			parts := strings.SplitN(rest, ".", 2)
			if len(parts) < 2 {
				continue
			}
			if idx, err := strconv.Atoi(parts[0]); err == nil && idx > maxIdx {
				maxIdx = idx
			}
		}
		if maxIdx < 0 {
			result[tp.Name] = []map[string]any{}
			continue
		}
		rows := make([]map[string]any, maxIdx+1)
		for i := range rows {
			rows[i] = make(map[string]any)
		}
		for key, vals := range r.Form {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := strings.TrimPrefix(key, prefix)
			parts := strings.SplitN(rest, ".", 2)
			if len(parts) < 2 {
				continue
			}
			idx, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			fieldName := parts[1]
			if len(vals) > 0 {
				rows[idx][fieldName] = vals[0]
			}
		}
		// filter empty rows (all fields blank) and convert types
		var cleaned []map[string]any
		for _, row := range rows {
			empty := true
			for _, f := range tp.Fields {
				if v, ok := row[f.Name]; ok && fmt.Sprintf("%v", v) != "" {
					empty = false
					break
				}
			}
			if !empty {
				converted := make(map[string]any, len(row))
				for _, f := range tp.Fields {
					raw := fmt.Sprintf("%v", row[f.Name])
					switch f.Type {
					case metadata.FieldTypeNumber:
						if n, err := strconv.ParseFloat(raw, 64); err == nil {
							converted[f.Name] = n
						} else {
							converted[f.Name] = raw
						}
					case metadata.FieldTypeBool:
						converted[f.Name] = raw == "true"
					default:
						converted[f.Name] = raw
					}
				}
				cleaned = append(cleaned, converted)
			}
		}
		result[tp.Name] = cleaned
	}
	return result
}

// parseListParams reads filter, search, sort and pagination URL params.
// listModeCookie — кука с per-сущностными предпочтениями режима списка
// (pages|feed). Значение — URL-escaped JSON map: "kind/name" → режим.
const listModeCookie = "ob_listmodes"

// resolveListMode возвращает true, если список показывать «лентой» (feed).
// Приоритет: явный ?lm=feed|pages (и тогда запоминаем в куку) > кука
// per-сущность > дефолт сущности (ListMode). По умолчанию — постранично.
func (s *Server) resolveListMode(w http.ResponseWriter, r *http.Request, entity *metadata.Entity) bool {
	key := strings.ToLower(string(entity.Kind)) + "/" + strings.ToLower(entity.Name)
	if lm := r.URL.Query().Get("lm"); lm == "feed" || lm == "pages" {
		setListModeCookie(w, r, key, lm)
		return lm == "feed"
	}
	if v, ok := readListModeCookie(r)[key]; ok {
		return v == "feed"
	}
	return entity.ListMode == "feed"
}

func readListModeCookie(r *http.Request) map[string]string {
	m := map[string]string{}
	c, err := r.Cookie(listModeCookie)
	if err != nil {
		return m
	}
	raw, err := url.QueryUnescape(c.Value)
	if err != nil {
		return m
	}
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

func setListModeCookie(w http.ResponseWriter, r *http.Request, key, mode string) {
	m := readListModeCookie(r)
	if m[key] == mode {
		return
	}
	m[key] = mode
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     listModeCookie,
		Value:    url.QueryEscape(string(b)),
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		SameSite: http.SameSiteLaxMode,
	})
}

// defaultLimit задаёт размер страницы по умолчанию (приходит из настроек базы
// _settings.ui.list_page_size; см. storage.GetListPageSize).
func parseListParams(r *http.Request, entity *metadata.Entity, defaultLimit int) storage.ListParams {
	q := r.URL.Query()
	params := storage.ListParams{
		Filters: make(map[string]storage.FilterValue),
		Sort:    q.Get("sort"),
		Dir:     q.Get("dir"),
		Search:  q.Get("q"),
	}

	// Pagination
	if defaultLimit <= 0 {
		defaultLimit = storage.DefaultListPageSize
	}
	limit := defaultLimit
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= storage.MaxListPageSize {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 1 {
		page = p
	}
	params.Limit = limit
	params.Offset = (page - 1) * limit
	if entity.Activity != nil {
		scope := strings.ToLower(strings.TrimSpace(q.Get("activity")))
		switch scope {
		case metadata.ActivityScopeActive, metadata.ActivityScopeInactive, metadata.ActivityScopeAll:
			params.ActivityScope = scope
		default:
			params.ActivityScope = entity.Activity.DefaultScope
			if params.ActivityScope == "" {
				params.ActivityScope = metadata.ActivityScopeActive
			}
		}
	}

	for _, f := range entity.Fields {
		if entity.Activity != nil && f.Name == entity.Activity.Field {
			continue
		}
		switch f.Type {
		case metadata.FieldTypeDate:
			from := q.Get("f." + f.Name + ".from")
			to := q.Get("f." + f.Name + ".to")
			if from != "" || to != "" {
				params.Filters[f.Name] = storage.FilterValue{From: from, To: to}
			}
		default:
			val := q.Get("f." + f.Name)
			if val != "" {
				params.Filters[f.Name] = storage.FilterValue{Value: val}
			}
		}
	}
	return params
}

// generateNumber returns the next document number.
// Uses the entity's Numerator config if present; falls back to legacy NextNum.
func (s *Server) generateNumber(ctx context.Context, entity *metadata.Entity, fields map[string]any) string {
	if entity.Numerator != nil {
		num := entity.Numerator
		periodKey := storage.ComputePeriodKey(num, fields)
		if n, err := s.store.NextNumber(ctx, entity.Name, periodKey); err == nil {
			return storage.FormatNumber(num.Prefix, num.Length, n)
		}
	}
	// legacy fallback: plain sequential number
	if n, err := s.store.NextNum(ctx, entity.Name); err == nil {
		return fmt.Sprintf("%06d", n)
	}
	return ""
}

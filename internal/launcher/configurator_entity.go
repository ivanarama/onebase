package launcher

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"gopkg.in/yaml.v3"
)

func (h *handler) configuratorSaveModule(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	entityName := r.FormValue("entity")
	moduleType := r.FormValue("module_type")
	source := r.FormValue("source")
	if !validObjectName(entityName) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя объекта")
		renderCfg(w, r, data)
		return
	}

	var filename string
	switch moduleType {
	case "posting":
		filename = entityToPostingFilename(entityName)
	case "manager":
		filename = entityToManagerFilename(entityName)
	default:
		filename = entityToFilename(entityName)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			saveErr = err
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, "src/"+filename, []byte(source))
		}
	} else {
		saveErr = h.writeConfigFileRaw(r.Context(), b, "src/"+filename, []byte(source))
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.ModuleSaved = true
		data.ModuleSavedEntity = entityName
	}
	renderCfg(w, r, data)
}

// entityToFilename converts "ПоступлениеТоваров" → "поступлениеТоваров.os"
func entityToFilename(name string) string {
	if name == "" {
		return ".os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".os"
}

// entityToPostingFilename converts "ПоступлениеТоваров" → "поступлениеТоваров.posting.os"
func entityToPostingFilename(name string) string {
	if name == "" {
		return ".posting.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".posting.os"
}

// entityToManagerFilename converts "ПоступлениеТоваров" → "поступлениеТоваров.manager.os"
func entityToManagerFilename(name string) string {
	if name == "" {
		return ".manager.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".manager.os"
}

// ── field-type save ───────────────────────────────────────────────────────────

func findEntityFilePath(dir, entityName string) (string, error) {
	// Сканируем все папки с YAML-объектами, чтобы контекстное удаление работало
	// не только для справочников/документов, но и для регистров, перечислений,
	// отчётов и подсистем (раньше находились лишь catalogs/documents).
	for _, sub := range []string{"catalogs", "documents", "registers", "inforegisters", "accountregisters", "enums", "reports", "subsystems"} {
		items, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, sub, item.Name())
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var hdr struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(data, &hdr) == nil && hdr.Name == entityName {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("entity %q not found", entityName)
}

func applyFieldEdits(ent *saveEntity, fields []saveField, tpFields map[string][]saveField, posting *bool, postCaption *string, postAndCloseHidden *bool, hierarchical *bool, basedOn *[]string, activity **saveActivity) {
	// Устойчивые id (план 81) переносим из прежнего состояния файла и выдаём
	// новым реквизитам — иначе редактор стирал бы их при каждом сохранении.
	ent.Fields = ensureFieldIDs(ent.Fields, fields)
	existingTP := make(map[string]bool, len(ent.TableParts))
	for i, tp := range ent.TableParts {
		existingTP[tp.Name] = true
		if f, ok := tpFields[tp.Name]; ok {
			ent.TableParts[i].Fields = ensureFieldIDs(tp.Fields, f)
		}
	}
	// Новые табличные части — ключи tpFields, которых ещё нет среди
	// существующих ТЧ. Раньше они молча терялись (цикл шёл только по
	// ent.TableParts), из-за чего «Добавить табличную часть» в конфигураторе не
	// сохранялось. Имена сортируем для детерминированного YAML (порядок обхода
	// ключей карты в Go не определён).
	var newTP []string
	for name := range tpFields {
		if !existingTP[name] {
			newTP = append(newTP, name)
		}
	}
	sort.Strings(newTP)
	for _, name := range newTP {
		ent.TableParts = append(ent.TableParts, saveTP{Name: name, Fields: ensureFieldIDs(nil, tpFields[name])})
	}
	if posting != nil {
		ent.Posting = *posting
	}
	// post_caption / post_and_close_hidden — скалярные свойства документа
	// (issue #497). nil = «поле не пришло с формы, не трогать YAML».
	if postCaption != nil {
		ent.PostCaption = *postCaption
	}
	if postAndCloseHidden != nil {
		ent.PostAndCloseHidden = *postAndCloseHidden
	}
	if hierarchical != nil {
		ent.Hierarchical = *hierarchical
		// При сбросе иерархии — стираем и hierarchy_kind, чтобы в YAML
		// не оставался «фантомный» ключ без эффекта.
		if !*hierarchical {
			ent.HierarchyKind = ""
		}
	}
	if basedOn != nil {
		// nil-slice → based_on удаляется из YAML (omitempty); пустой
		// явный slice трактуем так же.
		if len(*basedOn) == 0 {
			ent.BasedOn = nil
		} else {
			ent.BasedOn = append([]string(nil), (*basedOn)...)
		}
	}
	if activity != nil {
		ent.Activity = *activity
	}
}

func saveEntityFieldsToFile(dir, entityName string, fields []saveField, tpFields map[string][]saveField, posting *bool, postCaption *string, postAndCloseHidden *bool, hierarchical *bool, basedOn *[]string, activity **saveActivity, objTitles *map[string]string) error {
	filePath, err := findEntityFilePath(dir, entityName)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var ent saveEntity
	if err := yaml.Unmarshal(raw, &ent); err != nil {
		return err
	}
	applyFieldEdits(&ent, fields, tpFields, posting, postCaption, postAndCloseHidden, hierarchical, basedOn, activity)
	if objTitles != nil {
		ent.Titles = *objTitles
	}
	out, err := yaml.Marshal(&ent)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, out, fsmode.File)
}

func (h *handler) saveEntityFieldsToDB(ctx context.Context, b *Base, entityName string, fields []saveField, tpFields map[string][]saveField, posting *bool, postCaption *string, postAndCloseHidden *bool, hierarchical *bool, basedOn *[]string, activity **saveActivity, objTitles *map[string]string) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(ctx,
		`SELECT path, content FROM _onebase_config WHERE path LIKE 'catalogs/%.yaml' OR path LIKE 'documents/%.yaml'`)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var targetPath string
	var ent saveEntity
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var e saveEntity
		if yaml.Unmarshal(content, &e) == nil && e.Name == entityName {
			targetPath = p
			ent = e
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("entity %q not found in DB config", entityName)
	}

	applyFieldEdits(&ent, fields, tpFields, posting, postCaption, postAndCloseHidden, hierarchical, basedOn, activity)
	if objTitles != nil {
		ent.Titles = *objTitles
	}
	out, err := yaml.Marshal(&ent)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
}

func (h *handler) configuratorSaveForm(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}

	entityName := r.FormValue("entity")
	// Имя сущности приходит из формы и ниже попадает в путь, по которому файл
	// читается и ПЕРЕЗАПИСЫВАЕТСЯ. nameToFilename только приводит к нижнему
	// регистру и разделители не вычищает, а filepath.Join схлопывает «..» — то
	// есть без этой проверки «entity=../../foo» уводило запись за пределы
	// каталога проекта. Соседние обработчики (модуль, обработка, отчёт) такую
	// проверку делают; здесь её не было.
	if !validObjectName(entityName) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя объекта")
		renderCfg(w, r, data)
		return
	}
	dir := b.Path
	if b.ConfigSource == "database" {
		dir, err = workspacePath(b.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}

	// Find entity YAML file.
	//
	// SafeJoin — второй рубеж: он подтверждает, что собранный путь остался
	// внутри dir, даже если проверка имени выше однажды ослабнет.
	entityDir := ""
	for _, sub := range []string{"catalogs", "documents"} {
		p, jerr := configdb.SafeJoin(dir, sub+"/"+nameToFilename(entityName)+".yaml")
		if jerr != nil {
			continue
		}
		if _, e := os.Stat(p); e == nil { //nolint:gosec // G703: p построен SafeJoin, имя проверено validObjectName выше
			entityDir = sub
			break
		}
	}
	if entityDir == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Файл сущности не найден") + ": " + entityName
		renderCfg(w, r, data)
		return
	}

	filePath, jerr := configdb.SafeJoin(dir, entityDir+"/"+nameToFilename(entityName)+".yaml")
	if jerr != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя объекта")
		renderCfg(w, r, data)
		return
	}
	raw, err := os.ReadFile(filePath) //nolint:gosec // G703: filePath построен SafeJoin, имя проверено validObjectName
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка чтения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Parse as generic map, update list_form and item_form
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка YAML") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Build list_form from lf.N.name + lf.N.vis
	var listForm []string
	for i := 0; ; i++ {
		name := r.FormValue(fmt.Sprintf("lf.%d.name", i))
		if name == "" {
			break
		}
		vis := r.FormValue(fmt.Sprintf("lf.%d.vis", i))
		if vis == "1" {
			listForm = append(listForm, name)
		}
	}
	if len(listForm) > 0 {
		doc["list_form"] = listForm
	} else {
		delete(doc, "list_form")
	}

	// Build item_form from ef.N.name + ef.N.vis
	var itemForm []string
	for i := 0; ; i++ {
		name := r.FormValue(fmt.Sprintf("ef.%d.name", i))
		if name == "" {
			// Try table part fields
			name = r.FormValue(fmt.Sprintf("ef.tp0.%d.name", i))
		}
		if name == "" {
			break
		}
		vis := r.FormValue(fmt.Sprintf("ef.%d.vis", i))
		if vis == "" {
			vis = r.FormValue(fmt.Sprintf("ef.tp0.%d.vis", i))
		}
		if vis == "1" {
			itemForm = append(itemForm, name)
		}
	}
	// Also check table part fields with any index
	for tpJ := 0; ; tpJ++ {
		foundAny := false
		for fi := 0; ; fi++ {
			name := r.FormValue(fmt.Sprintf("ef.tp%d.%d.name", tpJ, fi))
			if name == "" {
				break
			}
			foundAny = true
			vis := r.FormValue(fmt.Sprintf("ef.tp%d.%d.vis", tpJ, fi))
			if vis == "1" {
				itemForm = append(itemForm, name)
			}
		}
		if !foundAny {
			break
		}
	}
	if len(itemForm) > 0 {
		doc["item_form"] = itemForm
	} else {
		delete(doc, "item_form")
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка сериализации") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if err := os.WriteFile(filePath, out, fsmode.File); err != nil { //nolint:gosec // G703: filePath построен SafeJoin, имя проверено validObjectName
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	fresh := h.loadCfgData(r.Context(), b, "tree")
	fresh.FieldsSaved = true
	fresh.FieldsSavedEntity = entityName
	renderCfg(w, r, fresh)
}

// numberTypeWithSpec превращает тип "number" в инлайн-нотацию "number(L,P)",
// если на форме заданы разрядность (Длина) и точность. Для остальных типов
// (включая уже собранные "reference:X"/"enum:X") возвращает typ без изменений.
func numberTypeWithSpec(typ, lengthVal, scaleVal string) string {
	if typ != "number" {
		return typ
	}
	l, _ := strconv.Atoi(strings.TrimSpace(lengthVal))
	if l <= 0 {
		return typ
	}
	s, _ := strconv.Atoi(strings.TrimSpace(scaleVal))
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("number(%d,%d)", l, s)
}

// formRowIndices возвращает отсортированные числовые индексы строк, присланных
// под базовым ключом base: ключи вида <base>.<idx>.name. Индексы на фронте
// выдаёт глобальный счётчик (_cfgNewFieldIdx), поэтому непрерывности нет —
// собираем фактически присланные. Точка после индекса исключает ложные
// совпадения имён-префиксов (например «new_tp.Цен.field» против «...Цены...»).
func formRowIndices(r *http.Request, base string) []int {
	prefix := base + "."
	var idxs []int
	for k := range r.Form {
		if !strings.HasPrefix(k, prefix) || !strings.HasSuffix(k, ".name") {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(k, prefix), ".name")
		if n, err := strconv.Atoi(mid); err == nil {
			idxs = append(idxs, n)
		}
	}
	sort.Ints(idxs)
	return idxs
}

// anyOrNil превращает nil-карту в нетипизированный nil, чтобы setYAMLMapField
// удалил ключ (типизированный nil map, обёрнутый в any, != nil).
func anyOrNil(m map[string]string) any {
	if m == nil {
		return nil
	}
	return m
}

// formHasMapField сообщает, содержала ли форма блок ключей "<prefix>.*"
// (хотя бы один, пусть и с пустым значением). Отличает «блок переводов был
// отрендерен и отправлен» (применяем, включая очистку) от «блока не было»
// (не трогаем существующее). Используется для round-trip/точечных сохранений.
func formHasMapField(r *http.Request, prefix string) bool {
	pfx := prefix + "."
	for key := range r.Form {
		if strings.HasPrefix(key, pfx) {
			return true
		}
	}
	return false
}

// parseMapForm читает значения формы вида "<prefix>.<lang>" в map[lang]value.
// Скан по ключам формы (а не по списку языков) — чтобы не зависеть от бандла
// локализации. Пропускает: базовый язык ru (он в title/label), пустые значения
// и остатки с точкой (защита от пересечения с вложенными префиксами, напр.
// "titles." не должен ловить "field.0.titles.en"). nil при пустом результате —
// тогда omitempty / setYAMLMapField(nil) убирают ключ из YAML.
func parseMapForm(r *http.Request, prefix string) map[string]string {
	pfx := prefix + "."
	out := map[string]string{}
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, pfx) || len(vals) == 0 {
			continue
		}
		lang := strings.TrimPrefix(key, pfx)
		if lang == "" || lang == "ru" || strings.Contains(lang, ".") {
			continue
		}
		if v := strings.TrimSpace(vals[0]); v != "" {
			out[lang] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseRegSection читает секцию полей регистра/плана счетов из формы. Существующие
// строки приходят как <prefix>.<i>.{name,type,length,scale,ref} (обрыв на первом
// пустом), добавленные кнопкой «+ Добавить» — как new_<prefix>.<idx>.* (пропуск
// пустых, индексы из глобального счётчика). Тип числа кодируется number(L,P) через
// numberTypeWithSpec; reference/enum → typ:ref — как в редакторе реквизитов
// сущности. Раньше точность и добавленные строки терялись.
func parseRegSection(r *http.Request, prefix string) []saveField {
	var fields []saveField
	add := func(keyBase string) {
		name := strings.TrimSpace(r.FormValue(keyBase + ".name"))
		if name == "" {
			return
		}
		typ := r.FormValue(keyBase + ".type")
		ref := r.FormValue(keyBase + ".ref")
		if (typ == "reference" || typ == "enum") && ref != "" {
			typ = typ + ":" + ref
		}
		typ = numberTypeWithSpec(typ, r.FormValue(keyBase+".length"), r.FormValue(keyBase+".scale"))
		sf := saveField{Name: name, Type: typ}
		sf.Titles = parseMapForm(r, keyBase+".titles")
		fields = append(fields, sf)
	}
	for _, i := range formRowIndices(r, prefix) {
		add(fmt.Sprintf("%s.%d", prefix, i))
	}
	for _, i := range formRowIndices(r, "new_"+prefix) {
		add(fmt.Sprintf("new_%s.%d", prefix, i))
	}
	return fields
}

// buildTPSaveField собирает saveField строки ТЧ из значений формы с базовым
// ключом keyBase: тип, разрядность числа number(L,P), ссылку/перечисление.
// Возвращает непустую строку ошибки, если для reference/enum не выбран объект.
func buildTPSaveField(r *http.Request, lang, keyBase, tpName, name string) (saveField, string) {
	typ := r.FormValue(keyBase + ".type")
	ref := r.FormValue(keyBase + ".ref")
	if typ == "reference" || typ == "enum" {
		if ref == "" {
			kind := "объект для ссылки"
			if typ == "enum" {
				kind = "перечисление"
			}
			return saveField{}, fmt.Sprintf(tr(lang, "Поле «%s.%s»: выберите %s"), tpName, name, kind)
		}
		typ = typ + ":" + ref
	}
	typ = numberTypeWithSpec(typ, r.FormValue(keyBase+".length"), r.FormValue(keyBase+".scale"))
	sf := saveField{Name: name, Type: typ}
	sf.Titles = parseMapForm(r, keyBase+".titles")
	return sf, ""
}

func saveFieldsHasBool(fields []saveField, name string) bool {
	for _, f := range fields {
		if f.Name == name && f.Type == "bool" {
			return true
		}
	}
	return false
}

func (h *handler) configuratorSaveFields(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	entityName := r.FormValue("entity")
	tpNames := r.Form["tp_names"]
	entityKind := r.FormValue("entity_kind")

	// read posting checkbox (only meaningful for documents)
	var posting *bool
	if entityKind == "Документ" {
		v := r.FormValue("posting") == "true"
		posting = &v
	}
	// Подпись кнопки проведения и скрытие «… и закрыть» — только документ (issue #497).
	var postCaption *string
	var postAndCloseHidden *bool
	if entityKind == "Документ" {
		pc := strings.TrimSpace(r.FormValue("post_caption"))
		postCaption = &pc
		hide := r.FormValue("post_and_close_hidden") == "true"
		postAndCloseHidden = &hide
	}
	// «Иерархический» имеет смысл только для справочников. Передаём
	// указатель — nil означает «не трогать поле в YAML», иначе
	// applyFieldEdits перепишет ent.Hierarchical явным значением.
	var hierarchical *bool
	if entityKind == "Справочник" {
		v := r.FormValue("hierarchical") == "true"
		hierarchical = &v
	}
	// Ввод на основании (Plan 38): список источников приходит как
	// based_on[] = ИмяСущности — multi-select на форме. Маркер «based_on_present»
	// отличает «поле не было на форме» (не трогать YAML) от «оба чекбокса
	// сняты» (очистить based_on). Без маркера было бы невозможно очистить
	// based_on через UI — пустой slice выглядел бы так же, как отсутствующее
	// поле.
	var basedOn *[]string
	if r.FormValue("based_on_present") == "1" {
		vals := r.Form["based_on"]
		clean := make([]string, 0, len(vals))
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v != "" {
				clean = append(clean, v)
			}
		}
		basedOn = &clean
	}

	var fields []saveField
	for _, i := range formRowIndices(r, "field") {
		name := r.FormValue(fmt.Sprintf("field.%d.name", i))
		if strings.TrimSpace(name) == "" {
			continue
		}
		typ := r.FormValue(fmt.Sprintf("field.%d.type", i))
		ref := r.FormValue(fmt.Sprintf("field.%d.ref", i))
		if typ == "reference" || typ == "enum" {
			if ref == "" {
				data := h.loadCfgData(r.Context(), b, "tree")
				kind := "объект для ссылки"
				if typ == "enum" {
					kind = "перечисление"
				}
				data.Error = fmt.Sprintf(tr(lang, "Поле «%s»: выберите %s"), name, kind)
				renderCfg(w, r, data)
				return
			}
			typ = typ + ":" + ref
		}
		typ = numberTypeWithSpec(typ, r.FormValue(fmt.Sprintf("field.%d.length", i)), r.FormValue(fmt.Sprintf("field.%d.scale", i)))
		sf := saveField{Name: name, Type: typ}
		sf.Titles = parseMapForm(r, fmt.Sprintf("field.%d.titles", i))
		// allow_inline_create — пишем только если значение отличается от дефолта
		// контекста (true в шапке). Шаблон отрисовывает hidden inline_present=1
		// только для ссылочных полей, поэтому маркер косвенно фильтрует тип.
		if r.FormValue(fmt.Sprintf("field.%d.inline_present", i)) == "1" {
			allow := r.FormValue(fmt.Sprintf("field.%d.inline_allow", i)) == "1"
			if !allow {
				f := false
				sf.AllowInlineCreate = &f
			}
		}
		fields = append(fields, sf)
	}
	// new fields added via "+ Добавить поле"
	for _, i := range formRowIndices(r, "new_field") {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("new_field.%d.name", i)))
		if name == "" {
			continue
		}
		typ := r.FormValue(fmt.Sprintf("new_field.%d.type", i))
		ref := r.FormValue(fmt.Sprintf("new_field.%d.ref", i))
		if typ == "reference" || typ == "enum" {
			if ref == "" {
				data := h.loadCfgData(r.Context(), b, "tree")
				kind := "объект для ссылки"
				if typ == "enum" {
					kind = "перечисление"
				}
				data.Error = fmt.Sprintf(tr(lang, "Поле «%s»: выберите %s"), name, kind)
				renderCfg(w, r, data)
				return
			}
			typ = typ + ":" + ref
		}
		typ = numberTypeWithSpec(typ, r.FormValue(fmt.Sprintf("new_field.%d.length", i)), r.FormValue(fmt.Sprintf("new_field.%d.scale", i)))
		fields = append(fields, saveField{Name: name, Type: typ, Titles: parseMapForm(r, fmt.Sprintf("new_field.%d.titles", i))})
	}

	var activity **saveActivity
	if entityKind == "Справочник" && r.FormValue("activity_present") == "1" {
		var target *saveActivity
		if r.FormValue("activity_enabled") == "1" {
			fieldName := strings.TrimSpace(r.FormValue("activity_field"))
			if fieldName == "" {
				data := h.loadCfgData(r.Context(), b, "tree")
				data.Error = tr(lang, "Выберите булевый реквизит активности")
				renderCfg(w, r, data)
				return
			}
			if !saveFieldsHasBool(fields, fieldName) {
				data := h.loadCfgData(r.Context(), b, "tree")
				data.Error = fmt.Sprintf(tr(lang, "Реквизит активности «%s» должен иметь тип bool"), fieldName)
				renderCfg(w, r, data)
				return
			}
			scope := strings.ToLower(strings.TrimSpace(r.FormValue("activity_default_scope")))
			if scope != "all" {
				scope = "active"
			}
			hide := r.FormValue("activity_hide_from_choice") == "1"
			target = &saveActivity{Field: fieldName, DefaultScope: scope, HideFromChoice: &hide}
		}
		activity = &target
	}

	tpFields := make(map[string][]saveField)
	for _, tpName := range tpNames {
		var f []saveField
		for _, i := range formRowIndices(r, "tp."+tpName+".field") {
			name := r.FormValue(fmt.Sprintf("tp.%s.field.%d.name", tpName, i))
			if strings.TrimSpace(name) == "" {
				continue
			}
			typ := r.FormValue(fmt.Sprintf("tp.%s.field.%d.type", tpName, i))
			ref := r.FormValue(fmt.Sprintf("tp.%s.field.%d.ref", tpName, i))
			if typ == "reference" || typ == "enum" {
				if ref == "" {
					data := h.loadCfgData(r.Context(), b, "tree")
					kind := "объект для ссылки"
					if typ == "enum" {
						kind = "перечисление"
					}
					data.Error = fmt.Sprintf(tr(lang, "Поле «%s.%s»: выберите %s"), tpName, name, kind)
					renderCfg(w, r, data)
					return
				}
				typ = typ + ":" + ref
			}
			typ = numberTypeWithSpec(typ, r.FormValue(fmt.Sprintf("tp.%s.field.%d.length", tpName, i)), r.FormValue(fmt.Sprintf("tp.%s.field.%d.scale", tpName, i)))
			sf := saveField{Name: name, Type: typ}
			sf.Titles = parseMapForm(r, fmt.Sprintf("tp.%s.field.%d.titles", tpName, i))
			// В ТЧ дефолт allow_inline_create = false; пишем только если
			// чекбокс установлен (отличие от дефолта).
			if r.FormValue(fmt.Sprintf("tp.%s.field.%d.inline_present", tpName, i)) == "1" {
				if r.FormValue(fmt.Sprintf("tp.%s.field.%d.inline_allow", tpName, i)) == "1" {
					t := true
					sf.AllowInlineCreate = &t
				}
			}
			f = append(f, sf)
		}
		tpFields[tpName] = f
	}
	// Новые табличные части («+ Добавить табличную часть») приходят маркером
	// new_tp_name=<Имя>. Поля, добавленные кнопкой «+ Добавить поле» — и в
	// новые, и в СУЩЕСТВУЮЩИЕ ТЧ — приходят под именным префиксом
	// new_tp.<Имя>.field.<idx>.* (idx — глобальный счётчик фронта, непрерывности
	// нет). Раньше бэкенд читал new_tp.<число>.idx, а имя клал отдельным ключом —
	// поэтому и новые ТЧ, и реквизиты, дописанные в существующую ТЧ, терялись.
	var newTPOrder []string
	seenNewTP := make(map[string]bool)
	for _, raw := range r.Form["new_tp_name"] {
		name := strings.TrimSpace(raw)
		if name == "" || seenNewTP[name] {
			continue
		}
		seenNewTP[name] = true
		newTPOrder = append(newTPOrder, name)
		if _, ok := tpFields[name]; !ok {
			tpFields[name] = nil // пустая новая ТЧ всё равно должна создаться
		}
	}
	// Дочитываем добавленные строки и дописываем к нужной ТЧ — существующей (из
	// tp_names) или только что созданной (из new_tp_name).
	appendDone := make(map[string]bool)
	for _, tpName := range append(append([]string{}, tpNames...), newTPOrder...) {
		if appendDone[tpName] {
			continue
		}
		appendDone[tpName] = true
		for _, i := range formRowIndices(r, "new_tp."+tpName+".field") {
			keyBase := fmt.Sprintf("new_tp.%s.field.%d", tpName, i)
			name := strings.TrimSpace(r.FormValue(keyBase + ".name"))
			if name == "" {
				continue
			}
			sf, errMsg := buildTPSaveField(r, lang, keyBase, tpName, name)
			if errMsg != "" {
				data := h.loadCfgData(r.Context(), b, "tree")
				data.Error = errMsg
				renderCfg(w, r, data)
				return
			}
			tpFields[tpName] = append(tpFields[tpName], sf)
		}
	}

	var objTitles *map[string]string
	if formHasMapField(r, "titles") {
		tm := parseMapForm(r, "titles")
		objTitles = &tm
	}

	if err := validateEntityFieldEdit(entityName, entityKind, fields, tpFields); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка проверки") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveEntityFieldsToDB(r.Context(), b, entityName, fields, tpFields, posting, postCaption, postAndCloseHidden, hierarchical, basedOn, activity, objTitles)
	} else {
		saveErr = saveEntityFieldsToFile(b.Path, entityName, fields, tpFields, posting, postCaption, postAndCloseHidden, hierarchical, basedOn, activity, objTitles)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = entityName
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorDeleteEntity(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	entityName := r.FormValue("entity")
	if !validObjectName(entityName) {
		http.Error(w, "valid entity name required", 400)
		return
	}
	objectKind := r.FormValue("kind")

	delErr := h.deleteConfiguratorObject(r, b, objectKind, entityName)
	data := h.loadCfgData(r.Context(), b, "tree")
	if delErr != nil {
		data.Error = tr(lang, "Ошибка удаления") + ": " + errText(r, delErr)
	} else {
		data.SavedMessage = tr(lang, "Сущность «") + entityName + tr(lang, "» удалена")
	}
	renderCfg(w, r, data)
}

type configuratorDeleteSpec struct {
	metadataDirs []string
	sourceSuffix []string
	forms        bool
}

var configuratorDeleteSpecs = map[string]configuratorDeleteSpec{
	"catalog":    {metadataDirs: []string{"catalogs"}, sourceSuffix: []string{".manager.os", ".os"}, forms: true},
	"document":   {metadataDirs: []string{"documents"}, sourceSuffix: []string{".posting.os", ".manager.os", ".os"}, forms: true},
	"entity":     {metadataDirs: []string{"catalogs", "documents"}, sourceSuffix: []string{".posting.os", ".manager.os", ".os"}, forms: true},
	"register":   {metadataDirs: []string{"registers"}},
	"inforeg":    {metadataDirs: []string{"inforegs"}},
	"accountreg": {metadataDirs: []string{"accountregs"}},
	"enum":       {metadataDirs: []string{"enums"}},
	"report":     {metadataDirs: []string{"reports"}, sourceSuffix: []string{".rep.os"}},
	"processor":  {metadataDirs: []string{"processors"}, sourceSuffix: []string{".proc.layout.yaml", ".proc.os"}, forms: true},
	"module":     {sourceSuffix: []string{".module.os"}},
	"printform":  {metadataDirs: []string{"printforms"}},
	"subsystem":  {metadataDirs: []string{"subsystems"}},
}

var configuratorDeleteKindFallback = []string{
	"catalog", "document", "register", "inforeg", "accountreg", "enum",
	"report", "processor", "module", "printform", "subsystem",
}

func (h *handler) deleteConfiguratorObject(r *http.Request, b *Base, kind, name string) error {
	files, err := h.listConfiguratorFiles(r.Context(), b)
	if err != nil {
		return err
	}
	paths, resolvedKind, err := configuratorObjectDeletePaths(files, kind, name)
	if err != nil {
		return err
	}
	return deleteConfigFilesWithVersion(r, h, b, paths, configdb.VersionOptions{
		AuthorLogin: cfgLogin(r.Context()),
		Message:     "delete " + resolvedKind + " " + name,
	})
}

// listConfiguratorFiles читает снимок конфигурации и закрывает соединение до
// начала удаления. Раньше deleteEntityFromDB вызывал Repo.DeleteFile, пока
// SELECT-курсор ещё держал единственное SQLite-соединение, и навсегда ждал
// свободного connection slot. В PostgreSQL результат зависел от размера пула.
func (h *handler) listConfiguratorFiles(ctx context.Context, b *Base) ([]configdb.ConfigFile, error) {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		defer db.Close()
		return configdb.New(db).ListByPrefix(ctx, "")
	}

	var files []configdb.ConfigFile
	err := filepath.WalkDir(b.Path, func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".onebase", "backups", "node_modules", "workspace":
				if full != b.Path {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, err := filepath.Rel(b.Path, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := configdb.ValidatePath(rel); err != nil {
			return err
		}
		var content []byte
		ext := strings.ToLower(filepath.Ext(rel))
		if ext == ".yaml" || ext == ".yml" {
			content, err = os.ReadFile(full) //nolint:gosec // G122: обход идёт по каталогу проекта или по временному каталогу, который мы сами распаковали; переход на os.Root — отдельная задача, он меняет поведение
			if err != nil {
				return err
			}
		}
		files = append(files, configdb.ConfigFile{Path: rel, Content: content})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func configuratorObjectDeletePaths(files []configdb.ConfigFile, kind, name string) ([]string, string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		for _, candidate := range configuratorDeleteKindFallback {
			paths, _, err := configuratorObjectDeletePaths(files, candidate, name)
			if err == nil {
				return paths, candidate, nil
			}
		}
		return nil, "", i18nerr.Errorf("сущность %q не найдена", name)
	}
	spec, ok := configuratorDeleteSpecs[kind]
	if !ok {
		return nil, "", i18nerr.Errorf("Неизвестный тип объекта")
	}

	var metadataPaths []string
	for _, file := range files {
		rel := filepath.ToSlash(file.Path)
		dir, base := filepath.ToSlash(filepath.Dir(rel)), filepath.Base(rel)
		if !stringInSlice(dir, spec.metadataDirs) ||
			(!strings.HasSuffix(strings.ToLower(base), ".yaml") && !strings.HasSuffix(strings.ToLower(base), ".yml")) {
			continue
		}
		var hdr struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(file.Content, &hdr) == nil && strings.EqualFold(hdr.Name, name) {
			metadataPaths = append(metadataPaths, rel)
		}
	}
	if len(spec.metadataDirs) > 0 && len(metadataPaths) == 0 {
		return nil, "", i18nerr.Errorf("сущность %q не найдена", name)
	}

	associated := make(map[string]struct{})
	foundSource := false
	for _, file := range files {
		rel := filepath.ToSlash(file.Path)
		if configuratorSourceBelongsToObject(rel, name, spec.sourceSuffix) {
			associated[rel] = struct{}{}
			foundSource = true
			continue
		}
		if spec.forms && configuratorFormBelongsToObject(rel, name) {
			associated[rel] = struct{}{}
		}
	}
	if len(spec.metadataDirs) == 0 && !foundSource {
		return nil, "", i18nerr.Errorf("сущность %q не найдена", name)
	}

	sort.Strings(metadataPaths)
	associatedPaths := make([]string, 0, len(associated))
	for rel := range associated {
		if !stringInSlice(rel, metadataPaths) {
			associatedPaths = append(associatedPaths, rel)
		}
	}
	sort.Strings(associatedPaths)
	return append(metadataPaths, associatedPaths...), kind, nil
}

func configuratorSourceBelongsToObject(rel, name string, suffixes []string) bool {
	if filepath.ToSlash(filepath.Dir(rel)) != "src" {
		return false
	}
	base := filepath.Base(rel)
	for _, suffix := range suffixes {
		if !strings.HasSuffix(strings.ToLower(base), strings.ToLower(suffix)) {
			continue
		}
		stem := base[:len(base)-len(suffix)]
		if strings.EqualFold(stem, name) ||
			strings.EqualFold(strings.ReplaceAll(stem, "_", " "), name) {
			return true
		}
	}
	return false
}

func configuratorFormBelongsToObject(rel, name string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) >= 3 && parts[0] == "forms" {
		if strings.EqualFold(parts[1], name) ||
			strings.EqualFold(strings.ReplaceAll(parts[1], "_", " "), name) {
			return true
		}
	}
	if len(parts) != 2 || parts[0] != "src" || !strings.HasSuffix(strings.ToLower(parts[1]), ".form.os") {
		return false
	}
	stem := strings.TrimSuffix(parts[1], ".form.os")
	nameLower := strings.ToLower(name)
	stemLower := strings.ToLower(stem)
	return stemLower == nameLower || strings.HasPrefix(stemLower, nameLower+"_")
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

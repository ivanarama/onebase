package ui

// HTTP-обработчики обработок: форма параметров, запуск, managed-результат.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"golang.org/x/text/encoding/charmap"
)

func (s *Server) processorForm(w http.ResponseWriter, r *http.Request) {
	proc := s.getProcessor(w, r)
	if proc == nil {
		return
	}
	if !s.canRunExternalProc(r, proc) {
		s.renderForbidden(w, r)
		return
	}

	// Managed form path
	if mf := proc.ManagedForm(); mf != nil {
		virtEntity := processorVirtualEntity(proc)
		paramValues := map[string]string{}
		for _, p := range proc.Params {
			if p.Default != nil {
				paramValues[p.Name] = fmt.Sprintf("%v", paramDefaultValue(p.Default, p.Type))
			}
		}
		refOpts, _ := s.loadInitialRefOptions(r.Context(), virtEntity, paramValues)
		enumOpts := s.loadEnumOptions(virtEntity, s.resolveLang(r))
		for k, v := range processorEnumOptions(proc) {
			enumOpts[k] = v
		}
		data := map[string]any{
			"Entity":        virtEntity,
			"Form":          mf,
			"IsNew":         true,
			"Values":        paramValues,
			"RefOptions":    refOpts,
			"EnumOptions":   enumOpts,
			"TPRefOptions":  map[string]map[string][]map[string]any{},
			"TPEnumLabels":  map[string]map[string]map[string]string{},
			"TPEnumOrder":   map[string]map[string][]string{},
			"TPRefMeta":     map[string]map[string]any{},
			"TablePartRows": map[string][]map[string]any{},
		}
		s.setProcessorManagedContext(r, data, proc)
		s.prepareManagedFormData(r.Context(), data, mf)
		s.render(w, r, "page-managed-form", data)
		return
	}

	// Auto-generated form (legacy)
	paramValues := map[string]any{}
	for _, p := range proc.Params {
		if p.Default != nil {
			paramValues[p.Name] = paramDefaultValue(p.Default, p.Type)
		}
	}
	refOpts := s.loadProcessorRefOpts(r.Context(), proc.Params, paramValues)
	s.render(w, r, "page-processor", map[string]any{
		"Processor":          proc,
		"ParamValues":        paramValues,
		"RefOptions":         refOpts,
		"ProcessorRefEntity": processorRefEntities(proc.Params),
	})
}

// loadProcessorRefOpts returns select options for reference-typed processor params.
func (s *Server) loadProcessorRefOpts(ctx context.Context, params []processorpkg.Param, values map[string]any) map[string][]map[string]any {
	opts := make(map[string][]map[string]any)
	for _, p := range params {
		if !strings.HasPrefix(p.Type, "reference:") {
			continue
		}
		entityName := strings.TrimPrefix(p.Type, "reference:")
		entity := s.reg.GetEntity(entityName)
		if entity == nil {
			continue
		}
		rows, err := s.initialReferenceOptions(ctx, entity, refOptionsChoice, []string{refValueString(values[p.Name])})
		if err != nil {
			continue
		}
		opts[p.Name] = rows
	}
	return opts
}

func processorRefEntities(params []processorpkg.Param) map[string]string {
	out := make(map[string]string)
	for _, p := range params {
		if strings.HasPrefix(p.Type, "reference:") {
			out[p.Name] = strings.TrimPrefix(p.Type, "reference:")
		}
	}
	return out
}

// paramDefaultValue приводит значение default из YAML обработки к виду,
// пригодному для подстановки в форму параметров.
func paramDefaultValue(def any, typ string) any {
	switch typ {
	case "bool":
		switch d := def.(type) {
		case bool:
			return d
		case string:
			return d == "true" || d == "1" || strings.EqualFold(d, "да")
		default:
			return false
		}
	case "date":
		if t, ok := def.(time.Time); ok {
			return t.Format("2006-01-02")
		}
		return fmt.Sprint(def)
	default:
		return def
	}
}

func (s *Server) processorRun(w http.ResponseWriter, r *http.Request) {
	// Предел тела ставится РОВНО ОДИН раз. Раньше их было два: сначала
	// defaultFormMemoryBytes, следом limitMultipartRequest на предел вложений —
	// и связывал всегда первый, потому что пределы не композируются, вложенный
	// MaxBytesReader режет по МЕНЬШЕМУ. Из-за этого параметр обработки типа file
	// был обрезан мегабайтом вместо заявленных attachments.max_file_size_mb
	// (issue #674).
	//
	// Присваивание r.Body остаётся здесь, в теле обработчика: вынеси его в
	// хелпер — и gosec (G120) перестанет видеть предел, пометив каждый
	// r.FormValue ниже.
	proc := s.getProcessor(w, r)
	if proc == nil {
		return
	}
	if !s.canRunExternalProc(r, proc) {
		s.renderForbidden(w, r)
		return
	}
	maxSize := s.effectiveUploadLimit()
	requestControls := processorRequestControlsForForm(proc, proc.ManagedForm())
	if requestControls.formTablesErr != nil {
		http.Error(w, requestControls.formTablesErr.Error(), http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, processorFormBodyLimit(r, maxSize, requestControls))
	if proc.External {
		// Запуск внешней обработки (исполнение DSL-кода) всегда логируем.
		s.auditExtProcRun(r, proc.Name)
	}
	opCtx, finish, ok := s.beginOperation(r, opProcessorRun, proc.Name)
	if !ok {
		http.Error(w, "слишком много одновременно выполняемых обработок, повторите позже", http.StatusTooManyRequests)
		return
	}
	opStatus := "ok"
	defer func() { finish(opStatus, 0, false) }()

	if err := parseBoundedForm(r, 32<<20); err != nil {
		opStatus = "error"
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}
	paramValues, err := processorParamValuesFromRequest(
		r,
		proc.Params,
		maxSize,
		requestControls,
	)
	if err != nil {
		opStatus = "error"
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}

	procDecl := s.reg.GetProcedure(proc.Name, "Выполнить")
	if procDecl == nil {
		runErr := s.tr(s.resolveLang(r), "Процедура Выполнить() не найдена в src/") + strings.ToLower(string([]rune(proc.Name)[:1])) + string([]rune(proc.Name)[1:]) + ".proc.os"
		if proc.ManagedForm() != nil {
			s.renderProcessorManagedResult(w, r, proc, paramValues, nil, runErr)
		} else {
			refOpts := s.loadProcessorRefOpts(r.Context(), proc.Params, paramValues)
			s.render(w, r, "page-processor", map[string]any{
				"Processor":          proc,
				"ParamValues":        paramValues,
				"RefOptions":         refOpts,
				"ProcessorRefEntity": processorRefEntities(proc.Params),
				"RunError":           runErr,
			})
		}
		return
	}

	var messages []string

	paramsThis := &interpreter.MapThis{M: paramValues}
	mc := runtime.NewMovementsCollector("processor", uuid.Nil)
	dslCtx, cancelDSL := context.WithCancel(opCtx)
	defer cancelDSL()
	dslVars, txState := s.buildDSLVarsWithMessagesTx(dslCtx, mc, &messages)
	defer rollbackDSLExecution(txState)
	dslVars["Параметры"] = paramsThis
	interpreter.InjectMaket(dslVars, proc.Layout)
	// Параметры обработки связываем и с одноимёнными аргументами Выполнить —
	// см. interpreter.BindNamedArgs (#706).
	procArgs := interpreter.BindNamedArgs(procDecl, paramValues)
	var callErr error
	if timeout := processorSandboxTimeout(opCtx, s.operationTimeout(opProcessorRun)); timeout > 0 {
		_, callErr = s.interp.CallSandboxed(procDecl, paramsThis, procArgs,
			interpreter.SandboxProfile{Context: dslCtx, MaxWallClock: timeout}, dslVars)
	} else {
		_, callErr = s.interp.Call(procDecl, paramsThis, procArgs, dslVars)
	}
	callErr = finishDSLExecution(txState, callErr)

	var runErr string
	if callErr != nil {
		opStatus = operationStatus(opCtx, callErr)
		runErr = callErr.Error()
	}

	if proc.ManagedForm() != nil {
		s.renderProcessorManagedResult(w, r, proc, paramValues, messages, runErr)
	} else {
		refOpts := s.loadProcessorRefOpts(r.Context(), proc.Params, paramValues)
		s.render(w, r, "page-processor", map[string]any{
			"Processor":          proc,
			"ParamValues":        paramValues,
			"RefOptions":         refOpts,
			"ProcessorRefEntity": processorRefEntities(proc.Params),
			"Messages":           messages,
			"RunError":           runErr,
			"Ran":                true,
		})
	}
}

// renderProcessorManagedResult renders processor results via managed form template.
func (s *Server) renderProcessorManagedResult(w http.ResponseWriter, r *http.Request, proc *processorpkg.Processor, paramValues map[string]any, messages []string, runErr string) {
	virtEntity := processorVirtualEntity(proc)
	strValues := make(map[string]string, len(paramValues))
	for k, v := range paramValues {
		strValues[k] = fmt.Sprintf("%v", v)
	}
	refOpts, _ := s.loadInitialRefOptions(r.Context(), virtEntity, strValues)
	enumOpts := s.loadEnumOptions(virtEntity, s.resolveLang(r))
	for k, v := range processorEnumOptions(proc) {
		enumOpts[k] = v
	}
	data := map[string]any{
		"Entity":        virtEntity,
		"Form":          proc.ManagedForm(),
		"IsNew":         true,
		"Values":        strValues,
		"RefOptions":    refOpts,
		"EnumOptions":   enumOpts,
		"TPRefOptions":  map[string]map[string][]map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"Messages":      messages,
		"RunError":      runErr,
		"Ran":           true,
	}
	s.setProcessorManagedContext(r, data, proc)
	s.prepareManagedFormData(r.Context(), data, proc.ManagedForm())
	s.render(w, r, "page-managed-form", data)
}

// setProcessorManagedContext задаёт единый permission-контракт управляемой
// формы обработки. Виртуальная Entity имеет kind=catalog только ради общего
// pipeline полей и табличных частей; права catalog/<имя>/write к ней отношения
// не имеют. Редактирование параметров разрешено тем же processor/<имя>/run,
// которым защищены GET формы и POST запуска. Явный CanWrite также не даёт
// Server.render подставить право фиктивного справочника по умолчанию.
func (s *Server) setProcessorManagedContext(r *http.Request, data map[string]any, proc *processorpkg.Processor) {
	data["IsProcessor"] = true
	data["Processor"] = proc
	data["CanWrite"] = proc != nil && s.can(r, "processor", proc.Name, "run")
}

func (s *Server) getProcessor(w http.ResponseWriter, r *http.Request) *processorpkg.Processor {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	proc := s.reg.GetProcessor(name)
	if proc == nil {
		http.Error(w, "unknown processor: "+name, 404)
		return nil
	}
	if !s.requirePerm(w, r, "processor", proc.Name, "run") {
		return nil
	}
	return proc
}

// decodeUploadText tries UTF-8; falls back to Windows-1251.
func decodeUploadText(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	decoded, err := charmap.Windows1251.NewDecoder().Bytes(data)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

// processorFormBodyLimit accounts for URL encoding before ParseForm decodes
// the body. A byte can occupy three transport bytes (%XX), so valid declared
// file parameters near their per-file max must not be rejected by the outer
// reader merely because of transport expansion or because there is more than
// one file control. Only editable file controls rendered by the selected form
// contribute; a form without one keeps the normal small-form limit.
func processorFormBodyLimit(r *http.Request, maxFileSize int64, controls processorRequestControls) int64 {
	fileParams := make(map[string]bool, len(controls.fileInputs)+len(controls.fileContent))
	for key, names := range controls.fileInputs {
		if len(names) > 0 {
			fileParams[key] = true
		}
	}
	for key, names := range controls.fileContent {
		if len(names) > 0 {
			fileParams[key] = true
		}
	}
	fileCount := int64(len(fileParams))
	if fileCount == 0 {
		return defaultFormMemoryBytes
	}
	factor := fileCount
	if r != nil {
		mediaType := strings.TrimSpace(strings.SplitN(strings.ToLower(r.Header.Get("Content-Type")), ";", 2)[0])
		if mediaType == "application/x-www-form-urlencoded" {
			factor *= 3
		}
	}
	maxInt64 := int64(^uint64(0) >> 1)
	if maxFileSize > (maxInt64-uiMultipartOverhead)/factor {
		return maxInt64
	}
	return maxFileSize*factor + uiMultipartOverhead
}

// processorSandboxTimeout keeps parsing and DSL execution inside the one
// operation deadline created by beginOperation. Starting a fresh full sandbox
// timeout after parsing would let the request run for almost twice the limit.
func processorSandboxTimeout(ctx context.Context, configured time.Duration) time.Duration {
	return interpreter.ClampWallClock(ctx, configured)
}

const (
	processorParamPresencePrefix = "_ob_present_"
	processorFileContentPrefix   = "_fc_"
	processorServiceFieldPrefix  = "_ob_service_"
)

var processorServiceFields = []string{
	"_element",
	"_event",
	"_kind",
	"_id",
	"_pick_result",
	"_tp",
	"_tp_selected",
	"_tp_row",
	"_tp_row_number",
	"_tp_col",
	"_tp_col_index",
}

func processorParamPresenceName(params []processorpkg.Param, name string) string {
	return processorAllocatedAuxiliaryParamName(params, processorParamPresencePrefix, name)
}

func processorFileContentName(params []processorpkg.Param, name string) string {
	return processorAllocatedAuxiliaryParamName(params, processorFileContentPrefix, name)
}

func processorServiceFieldName(params []processorpkg.Param, name string) string {
	for _, p := range params {
		if strings.EqualFold(p.Name, name) {
			return processorAuxiliaryParamName(params, processorServiceFieldPrefix, strings.TrimPrefix(name, "_"))
		}
	}
	return name
}

func processorServiceFieldNames(params []processorpkg.Param) map[string]string {
	names := make(map[string]string, len(processorServiceFields))
	for _, name := range processorServiceFields {
		names[name] = processorServiceFieldName(params, name)
	}
	return names
}

// processorAuxiliaryParamName сохраняет привычное имя helper-поля, пока оно
// не совпадает с легальным параметром обработки. При совпадении выбирается
// ближайшее свободное имя: namespace параметров не резервируется, а сервер и
// шаблон получают один и тот же детерминированный результат.
func processorAuxiliaryParamName(params []processorpkg.Param, prefix, name string) string {
	declared := make(map[string]bool, len(params))
	for _, p := range params {
		declared[strings.ToLower(p.Name)] = true
	}
	aux := prefix + name
	for declared[strings.ToLower(aux)] {
		aux += "_"
	}
	return aux
}

// processorAllocatedAuxiliaryParamName additionally reserves helper names
// already allocated to preceding parameters. Otherwise Flag and Flag_ could
// both become _ob_present_Flag_ when _ob_present_Flag is a legal parameter.
func processorAllocatedAuxiliaryParamName(params []processorpkg.Param, prefix, name string) string {
	reserved := make(map[string]bool, len(params)*2)
	for _, p := range params {
		reserved[strings.ToLower(p.Name)] = true
	}
	for _, p := range params {
		aux := prefix + p.Name
		for reserved[strings.ToLower(aux)] {
			aux += "_"
		}
		if strings.EqualFold(p.Name, name) {
			return aux
		}
		reserved[strings.ToLower(aux)] = true
	}
	return processorAuxiliaryParamName(params, prefix, name)
}

// processorRequestControls описывает служебные поля, которые действительно
// отрисованы текущей формой обработки. Префиксы _ob_present_ и _fc_ сами по
// себе ничего не доказывают: такие имена разрешены у обычных параметров.
type processorRequestControls struct {
	paramFields      map[string][]string
	fileInputs       map[string][]string
	boolPresence     map[string][]string
	fileContent      map[string][]string
	formTables       map[string]metadata.FormTableDefinition
	tableAuthorities map[string]managedFormTableAuthority
	formTablesErr    error
}

// processorRequestControlsForForm строит allow-list служебных полей по форме,
// которую сервер только что отдал пользователю. nil означает legacy-форму:
// она рисует checkbox-marker для каждого bool-параметра и настоящий multipart
// input для file-параметра (без _fc_ textarea).
func processorRequestControlsForForm(proc *processorpkg.Processor, form *metadata.FormModule) processorRequestControls {
	params := proc.Params
	tableAuthorities, formTablesErr := managedFormTableAuthorities(form, proc.TableParts, true)
	var formTables map[string]metadata.FormTableDefinition
	if formTablesErr == nil {
		formTables, formTablesErr = editableFormTablesFromAuthorities(form, proc.TableParts, tableAuthorities)
	}
	controls := processorRequestControls{
		paramFields:      make(map[string][]string),
		fileInputs:       make(map[string][]string),
		boolPresence:     make(map[string][]string),
		fileContent:      make(map[string][]string),
		formTables:       formTables,
		tableAuthorities: tableAuthorities,
		formTablesErr:    formTablesErr,
	}
	if form == nil {
		for _, p := range params {
			key := strings.ToLower(p.Name)
			controls.paramFields[key] = append(controls.paramFields[key], p.Name)
			if p.Type == "bool" {
				controls.boolPresence[key] = append(controls.boolPresence[key], processorParamPresenceName(params, p.Name))
			}
			if p.Type == "file" {
				controls.fileInputs[key] = append(controls.fileInputs[key], p.Name)
			}
		}
		return controls
	}

	paramsByName := make(map[string]processorpkg.Param, len(params))
	for _, p := range params {
		paramsByName[strings.ToLower(p.Name)] = p
	}
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		// The table renderer only emits its own row controls and command
		// buttons; arbitrary scalar children are not browser form controls.
		if el.Kind == metadata.FormElementTablePart || visit.parentTablePart != nil {
			return
		}
		if visit.effectiveReadOnly || el.DataPath == "" || strings.Count(el.DataPath, ".") > 1 || !processorElementPostsParam(el.Kind) {
			return
		}
		fieldName := dpFieldName(el.DataPath)
		p, ok := paramsByName[strings.ToLower(fieldName)]
		if !ok {
			return
		}
		paramKey := strings.ToLower(p.Name)
		controls.paramFields[paramKey] = appendUniqueProcessorControl(
			controls.paramFields[paramKey], fieldName,
		)
		switch {
		case p.Type == "bool" && el.Kind == metadata.FormElementCheckbox:
			controls.boolPresence[paramKey] = appendUniqueProcessorControl(
				controls.boolPresence[paramKey], processorParamPresenceName(params, fieldName),
			)
		case p.Type == "file" && el.Kind == metadata.FormElementField && strings.EqualFold(el.Type, "file"):
			controls.fileInputs[paramKey] = appendUniqueProcessorControl(
				controls.fileInputs[paramKey], fieldName,
			)
			controls.fileContent[paramKey] = appendUniqueProcessorControl(
				controls.fileContent[paramKey], processorFileContentName(params, fieldName),
			)
		}
	})
	return controls
}

func processorElementPostsParam(kind metadata.FormElementType) bool {
	switch kind {
	case metadata.FormElementField,
		metadata.FormElementCodeField,
		metadata.FormElementInputList,
		metadata.FormElementCheckbox,
		metadata.FormElementDatePicker,
		metadata.FormElementSwitch:
		return true
	default:
		return false
	}
}

func appendUniqueProcessorControl(names []string, name string) []string {
	for _, existing := range names {
		if existing == name {
			return names
		}
	}
	return append(names, name)
}

// processorParamValuesFromRequest извлекает только реально переданные поля.
// Отсутствующий metadata-параметр не попадает в map и потому не затирает
// DSL-default при BindNamedArgs; явно переданное пустое значение остаётся
// полноценным значением (parseParamValue может представить его как nil).
func processorParamValuesFromRequest(
	r *http.Request,
	params []processorpkg.Param,
	maxFileSize int64,
	controls processorRequestControls,
) (map[string]any, error) {
	values := make(map[string]any)
	for _, p := range params {
		paramKey := strings.ToLower(p.Name)
		paramFields := controls.paramFields[paramKey]
		if p.Type == "file" {
			// Обычная страница обработки отправляет настоящий multipart-файл.
			// Managed obFire, напротив, преобразует FormData в urlencoded и
			// передаёт уже прочитанное браузером содержимое обычной строкой.
			// Не вызываем FormFile для urlencoded: он возвращает ErrNotMultipart.
			if r.MultipartForm != nil {
				for _, fieldName := range controls.fileInputs[paramKey] {
					file, _, err := r.FormFile(fieldName)
					if err == nil {
						data, readErr := readUploadedBytes(file, maxFileSize)
						closeRead("загруженный файл параметра", file)
						if readErr != nil {
							return nil, readErr
						}
						values[p.Name] = decodeUploadText(data)
						break
					}
					if !errors.Is(err, http.ErrMissingFile) {
						return nil, fmt.Errorf("файл-параметр %s: %w", p.Name, err)
					}
				}
				if _, present := values[p.Name]; present {
					continue
				}
			}

			// _fc_<name> — backing textarea файлового виджета. Текущий obFire
			// переносит его содержимое в поле <name>, но принимаем оба варианта:
			// это сохраняет прямой urlencoded-контракт и не путает содержимое с
			// текстовым полем пути, если клиент прислал оба.
			text, present := processorControlText(r, controls.fileContent[paramKey])
			if !present {
				text, present = processorControlText(r, paramFields)
			}
			if !present {
				continue
			}
			if int64(len([]byte(text))) > maxFileSize {
				return nil, errUploadTooLarge
			}
			values[p.Name] = text
			continue
		}

		text, present := processorControlText(r, paramFields)
		if !present {
			// HTML не отправляет unchecked checkbox. Presence-marker рисуется
			// только рядом с реально существующим bool-контролом, поэтому marker
			// означает явное false, а полное отсутствие custom-параметра оставляет
			// DSL-default нетронутым.
			if p.Type == "bool" {
				if _, rendered := processorControlText(r, controls.boolPresence[paramKey]); rendered {
					values[p.Name] = false
				}
			}
			continue
		}
		values[p.Name] = parseParamValue(text, p.Type)
	}
	return values, nil
}

// processorPostFormText читает только тело запроса. r.Form объединяет body с
// query string, поэтому через него ?_fc_X=... или ?_ob_present_X=... мог бы
// подделать состояние формы.
func processorPostFormText(r *http.Request, name string) (string, bool) {
	raw, present := processorPostFormValues(r, name)
	if !present {
		return "", false
	}
	if len(raw) == 0 {
		return "", true
	}
	return raw[0], true
}

func processorPostFormValues(r *http.Request, name string) ([]string, bool) {
	if r == nil || r.PostForm == nil {
		return nil, false
	}
	raw, present := r.PostForm[name]
	return raw, present
}

func processorControlText(r *http.Request, allowed []string) (string, bool) {
	for _, name := range allowed {
		if text, present := processorPostFormText(r, name); present {
			return text, true
		}
	}
	return "", false
}

// processorFormObjectFromRequest builds the object visible to a bound .form.os
// handler from the same validated parameter values as the processor call. It
// deliberately does not use buildObjectFromForm: that generic entity helper
// reads r.FormValue and therefore accepts query values and every entity field,
// including controls which this processor form did not render as editable.
func processorFormObjectFromRequest(
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	paramValues map[string]any,
	controls processorRequestControls,
) (*runtime.Object, error) {
	fields := make(map[string]any, len(paramValues))
	for name, value := range paramValues {
		fields[name] = value
	}
	rows, err := processorFormTableRowsFromRequest(r, entity, form, controls.formTables)
	if err != nil {
		return nil, err
	}
	return &runtime.Object{
		Type:          entity.Name,
		Kind:          entity.Kind,
		ID:            uuid.New(),
		Fields:        fields,
		TablePartRows: rows,
	}, nil
}

// processorFormTableRowsFromRequest exposes only body values belonging to
// editable table-part or ValueTable controls rendered by the current form. The
// normalized request lets the common parsers keep their type conversion without
// inheriting query parameters or arbitrary unrendered form tables.
func processorFormTableRowsFromRequest(
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	rendered map[string]metadata.FormTableDefinition,
) (map[string][]map[string]any, error) {
	body := make(url.Values)
	for postedName, values := range r.PostForm {
		for formName, definition := range rendered {
			jsonName := "tp_json." + formName
			if strings.EqualFold(postedName, jsonName) {
				body["tp_json."+definition.Name] = append(body["tp_json."+definition.Name], values...)
				break
			}
			legacyNamespace := "tp."
			if definition.Source == metadata.FormTableSourceValueTable {
				legacyNamespace = "vt."
			}
			legacyPrefix := legacyNamespace + formName + "."
			if len(postedName) >= len(legacyPrefix) && strings.EqualFold(postedName[:len(legacyPrefix)], legacyPrefix) {
				normalized := legacyNamespace + definition.Name + "." + postedName[len(legacyPrefix):]
				body[normalized] = append(body[normalized], values...)
				break
			}
		}
	}
	safeRequest := &http.Request{
		Method:   http.MethodPost,
		Header:   http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		Form:     body,
		PostForm: body,
	}
	rows, err := parseTablePartRowsForManagedForm(safeRequest, entity, form, true)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = make(map[string][]map[string]any)
	}
	valueTables, err := parseValueTableRowsForManagedForm(safeRequest, form, entity, true)
	if err != nil {
		return nil, err
	}
	for name, valueTableRows := range valueTables {
		rows[name] = valueTableRows
	}
	return rows, nil
}

// processorVirtualEntity создаёт виртуальную Entity из параметров обработки,
// чтобы managed-форма могла рендерить поля через стандартный pipeline.
func processorVirtualEntity(proc *processorpkg.Processor) *metadata.Entity {
	fields := make([]metadata.Field, 0, len(proc.Params))
	for _, p := range proc.Params {
		f := metadata.Field{
			Name:   p.Name,
			Title:  p.Label,
			Titles: p.Labels,
		}
		switch {
		case p.Type == "string", p.Type == "text":
			f.Type = metadata.FieldTypeString
		case p.Type == "number":
			f.Type = metadata.FieldTypeNumber
		case p.Type == "date":
			f.Type = metadata.FieldTypeDate
		case p.Type == "bool":
			f.Type = metadata.FieldTypeBool
		case p.Type == "choice":
			enumName := "_" + p.Name + "_choice"
			f.Type = metadata.FieldType("enum:" + enumName)
			f.EnumName = enumName
		case strings.HasPrefix(p.Type, "reference:"):
			f.Type = metadata.FieldType("reference:" + strings.TrimPrefix(p.Type, "reference:"))
			f.RefEntity = strings.TrimPrefix(p.Type, "reference:")
		default:
			f.Type = metadata.FieldTypeString
		}
		fields = append(fields, f)
	}
	return &metadata.Entity{
		Name:       proc.Name,
		Title:      proc.Title,
		Titles:     proc.Titles,
		Kind:       metadata.KindCatalog,
		Fields:     fields,
		TableParts: proc.TableParts,
	}
}

// processorEnumOptions возвращает synthetic enum options для choice-параметров
// обработки, дополняя результат loadEnumOptions.
func processorEnumOptions(proc *processorpkg.Processor) map[string][]EnumOption {
	opts := make(map[string][]EnumOption)
	for _, p := range proc.Params {
		if p.Type == "choice" && len(p.Options) > 0 {
			list := make([]EnumOption, 0, len(p.Options))
			for _, v := range p.Options {
				list = append(list, EnumOption{Value: v, Label: v})
			}
			opts[p.Name] = list
		}
	}
	return opts
}

func parseParamValue(s, typ string) any {
	if typ == "bool" {
		// Чекбокс: значение приходит в форме только когда флажок установлен.
		return s == "true" || s == "on" || s == "1" || strings.EqualFold(s, "да")
	}
	if s == "" {
		return nil
	}
	switch typ {
	case "date":
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return t
			}
		}
		return s
	case "number":
		if f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64); err == nil {
			return f
		}
		return s
	default:
		return s
	}
}

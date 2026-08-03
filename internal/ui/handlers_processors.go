package ui

// HTTP-обработчики обработок: форма параметров, запуск, managed-результат.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"context"
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
			"IsProcessor":   true,
			"Processor":     proc,
		}
		s.prepareManagedFormData(data, mf)
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
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	proc := s.getProcessor(w, r)
	if proc == nil {
		return
	}
	if !s.canRunExternalProc(r, proc) {
		s.renderForbidden(w, r)
		return
	}
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

	maxSize := s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, 32<<20); err != nil {
		opStatus = "error"
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}
	paramValues := map[string]any{}
	for _, p := range proc.Params {
		if p.Type == "file" {
			file, _, err := r.FormFile(p.Name)
			if err == nil {
				data, err := readUploadedBytes(file, maxSize)
				closeRead("загруженный файл параметра", file)
				if err != nil {
					opStatus = "error"
					http.Error(w, s.errText(r, err), uploadErrorStatus(err))
					return
				}
				paramValues[p.Name] = decodeUploadText(data)
				continue
			}
			paramValues[p.Name] = ""
			continue
		}
		paramValues[p.Name] = parseParamValue(r.FormValue(p.Name), p.Type)
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
	dslVars := s.buildDSLVarsWithMessages(opCtx, mc, &messages)
	dslVars["Параметры"] = paramsThis
	interpreter.InjectMaket(dslVars, proc.Layout)
	var err error
	if timeout := s.operationTimeout(opProcessorRun); timeout > 0 {
		err = s.interp.RunSandboxed(procDecl, paramsThis, interpreter.SandboxProfile{MaxWallClock: timeout}, nil, dslVars)
	} else {
		err = s.interp.Run(procDecl, paramsThis, dslVars)
	}

	var runErr string
	if err != nil {
		opStatus = operationStatus(opCtx, err)
		runErr = err.Error()
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
		"IsProcessor":   true,
		"Processor":     proc,
		"Messages":      messages,
		"RunError":      runErr,
		"Ran":           true,
	}
	s.prepareManagedFormData(data, proc.ManagedForm())
	s.render(w, r, "page-managed-form", data)
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

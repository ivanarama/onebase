package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
)

// FormLoader loads form modules from .os files
type FormLoader struct{}

// NewFormLoader creates form loader
func NewFormLoader() *FormLoader {
	return &FormLoader{}
}

// LoadEntityForms loads all form modules for an entity from src/ directory
func (fl *FormLoader) LoadEntityForms(srcDir, entityName string) ([]*metadata.FormModule, error) {
	var forms []*metadata.FormModule

	// Try to load object form (entity.form.os)
	objectFormFile := filepath.Join(srcDir, metadata.ObjectFormFileName(entityName))
	if form, err := fl.loadFormModule(objectFormFile, entityName, "ФормаОбъекта", "object"); err == nil {
		forms = append(forms, form)
	}

	// Try to load list form
	listFormFile := filepath.Join(srcDir, strings.ToLower(entityName)+"_list.form.os")
	if _, err := os.Stat(listFormFile); err == nil {
		if form, err := fl.loadFormModule(listFormFile, entityName, "ФормаСписка", "list"); err == nil {
			forms = append(forms, form)
		}
	}

	// Try to load choice form
	choiceFormFile := filepath.Join(srcDir, strings.ToLower(entityName)+"_choice.form.os")
	if _, err := os.Stat(choiceFormFile); err == nil {
		if form, err := fl.loadFormModule(choiceFormFile, entityName, "ФормаВыбора", "choice"); err == nil {
			forms = append(forms, form)
		}
	}

	// Load custom forms (pattern: entity_formname.form.os)
	files, err := os.ReadDir(srcDir)
	if err != nil {
		return forms, nil
	}

	prefix := strings.ToLower(entityName) + "_"
	suffix := ".form.os"

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			formName := strings.TrimPrefix(name, prefix)
			formName = strings.TrimSuffix(formName, suffix)
			formName = upperFirst(formName)

			if !metadata.IsStandardForm(formName) {
				fullPath := filepath.Join(srcDir, name)
				if form, err := fl.loadFormModule(fullPath, entityName, formName, "custom"); err == nil {
					forms = append(forms, form)
				}
			}
		}
	}

	return forms, nil
}

func upperFirst(s string) string {
	runes := []rune(s)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return string(runes)
}

// LoadFormModuleFromSource loads form module from DSL source
func (fl *FormLoader) LoadFormModuleFromSource(source, entityName, formName, kind string) (*metadata.FormModule, error) {
	// У исходника из памяти нет физического пути, но модулю всё равно нужна
	// стабильная identity: по ней интерпретатор ограничивает поиск соседних
	// процедур текущей формой. Угловые скобки явно отличают её от настоящего
	// файла в диагностике.
	sourceID := fmt.Sprintf("<form:%s/%s>", entityName, formName)
	return fl.parseFormModule(source, entityName, formName, kind, sourceID)
}

// parseFormModule разбирает модуль формы, сохраняя identity его источника в
// токенах AST. Для файлового пути это настоящее имя .form.os, для исходника из
// памяти — синтетическая, но стабильная identity из LoadFormModuleFromSource.
func (fl *FormLoader) parseFormModule(source, entityName, formName, kind, sourceID string) (*metadata.FormModule, error) {
	l := lexer.New(source, sourceID)
	p := parser.New(l)
	program, err := p.ParseProgram()
	if err != nil {
		return nil, err
	}

	form := &metadata.FormModule{
		EntityName: entityName,
		Name:       formName,
		Kind:       kind,
		Handlers:   make(map[metadata.FormEventType]string),
		Procedures: make(map[string]*metadata.FormProcedure),
		// Сохраняем полный AST модуля — рантайм событий формы достаёт
		// отсюда *ast.ProcedureDecl по имени для запуска через interp.Run.
		// Без этого FormProcedure.Body пустой (astToFormProcedure его не
		// заполняет), и события формы выполнить нельзя.
		ProgramAST: program,
	}

	// Parse procedures and extract handlers
	for _, proc := range program.Procedures {
		procName := proc.Name.Literal

		// Check if this is a form event handler
		if eventType := metadata.FormEventType(procName); eventType != "" {
			form.Handlers[eventType] = procName
		}

		// Store procedure for direct calls
		form.Procedures[procName] = fl.astToFormProcedure(proc)
	}

	return form, nil
}

// loadFormModule loads form module from file
func (fl *FormLoader) loadFormModule(filePath, entityName, formName, kind string) (*metadata.FormModule, error) {
	source, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	return fl.parseFormModule(string(source), entityName, formName, kind, filePath)
}

// astToFormProcedure converts AST procedure to FormProcedure
func (fl *FormLoader) astToFormProcedure(proc *ast.ProcedureDecl) *metadata.FormProcedure {
	nameLit := proc.Name.Literal
	isExport := strings.HasPrefix(nameLit, "Экспорт") || len(nameLit) > 0 && nameLit[0] == '_'
	fp := &metadata.FormProcedure{
		Name:     nameLit,
		Params:   []metadata.FormProcParam{},
		IsExport: isExport,
	}

	for _, param := range proc.Params {
		fp.Params = append(fp.Params, metadata.FormProcParam{
			Name: param.Literal,
			Type: "",
		})
	}

	return fp
}

// ParseFormStructure parses form structure from special comments or metadata
// In 1C, form structure is defined separately; here we support inline comments
func (fl *FormLoader) ParseFormStructure(source string) ([]*metadata.FormElement, error) {
	var elements []*metadata.FormElement

	// For now, return empty - form structure would be defined in YAML
	// or parsed from special comment blocks
	return elements, nil
}

// ExtractElementHandlers extracts element handlers from DSL source
// Example: // @Element ПолеТовар.ПриИзменении = ОбработкаТовара
func ExtractElementHandlers(source string) map[string]map[metadata.FormEventType]string {
	handlers := make(map[string]map[metadata.FormEventType]string)

	lines := strings.Split(source, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//") {
			continue
		}

		// Parse element handler directive
		// @Element ElementName.EventName = HandlerName
		if strings.Contains(line, "@Element") {
			parts := strings.Split(line, " ")
			for i, part := range parts {
				if part == "@Element" && i+1 < len(parts) {
					def := strings.TrimSpace(strings.Join(parts[i+1:], " "))
					if eqIdx := strings.Index(def, "="); eqIdx > 0 {
						left := strings.TrimSpace(def[:eqIdx])
						right := strings.TrimSpace(def[eqIdx+1:])

						dotIdx := strings.LastIndex(left, ".")
						if dotIdx > 0 {
							elemName := left[:dotIdx]
							eventName := left[dotIdx+1:]
							handlerName := right

							if handlers[elemName] == nil {
								handlers[elemName] = make(map[metadata.FormEventType]string)
							}
							handlers[elemName][metadata.FormEventType(eventName)] = handlerName
						}
					}
				}
			}
		}
	}

	return handlers
}

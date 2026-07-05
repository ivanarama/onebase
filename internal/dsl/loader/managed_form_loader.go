package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ManagedFormLoader загружает управляемые формы из <project>/forms/<entity>/*.form.yaml.
// В отличие от FormLoader, который читает .form.os с авто-генерируемой
// структурой, managed-форма имеет декларативное описание элементов в YAML
// и опциональный модуль с процедурами-обработчиками в соседнем .form.os.
//
// План 37 (foundation): загрузчик умеет читать YAML и опционально
// подключать процедуры из соседнего .form.os. UI-редактор и рендерер
// добавятся на этапах 3-4.
type ManagedFormLoader struct {
	innerFL *FormLoader // переиспользуем для парсинга .form.os
}

// NewManagedFormLoader создаёт загрузчик.
func NewManagedFormLoader() *ManagedFormLoader {
	return &ManagedFormLoader{innerFL: NewFormLoader()}
}

// LoadEntityForms ищет управляемые формы сущности в каталоге
//
//	<projectRoot>/forms/<entityLower>/*.form.yaml
//
// и возвращает их как FormModule с LayoutKind=managed.
// Если папки нет — возвращает (nil, nil) (это нормально: сущность работает
// в auto-generation-режиме).
func (mfl *ManagedFormLoader) LoadEntityForms(projectRoot, entityName string) ([]*metadata.FormModule, error) {
	entityDir := filepath.Join(projectRoot, "forms", strings.ToLower(entityName))
	entries, err := os.ReadDir(entityDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read forms dir %s: %w", entityDir, err)
	}

	var out []*metadata.FormModule
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".form.yaml") {
			continue
		}
		path := filepath.Join(entityDir, name)
		form, err := mfl.LoadFormFile(path, entityName)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}
		out = append(out, form)
	}
	return out, nil
}

// LoadFormFile читает одиночный .form.yaml.
// Параметр entityName используется только если в YAML не указано form.entity.
func (mfl *ManagedFormLoader) LoadFormFile(yamlPath, entityName string) (*metadata.FormModule, error) {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, err
	}

	form, err := mfl.parseYAML(data, entityName)
	if err != nil {
		return nil, err
	}

	// Если рядом лежит .form.os — подгружаем процедуры из него.
	// Имя модуля: тот же базовый, но с расширением .form.os.
	osPath := strings.TrimSuffix(yamlPath, ".form.yaml") + ".form.os"
	if _, statErr := os.Stat(osPath); statErr == nil {
		if err := mfl.attachProcedures(form, osPath); err != nil {
			return nil, fmt.Errorf("attach %s: %w", osPath, err)
		}
	}

	return form, nil
}

// formYAMLDoc — промежуточная структура для парсинга YAML. Поля совпадают
// с тем, что описано в Plans/37 раздел 3 (родной формат `.form.yaml`).
type formYAMLDoc struct {
	Schema string `yaml:"schema"`
	Form   struct {
		Name                   string            `yaml:"name"`
		Kind                   string            `yaml:"kind"`
		Entity                 string            `yaml:"entity"`
		Title                  map[string]string `yaml:"title"`
		OriginalID             string            `yaml:"original_id"`
		AutoSaveDataInSettings bool              `yaml:"auto_save_settings"`
		VerticalScroll         string            `yaml:"vertical_scroll"`
	} `yaml:"form"`
	Attributes            []*metadata.FormAttribute       `yaml:"attributes"`
	Commands              []*metadata.FormCommand         `yaml:"commands"`
	CommandBar            *metadata.FormCommandBar        `yaml:"command_bar"`
	Elements              []*metadata.FormElement         `yaml:"elements"`
	Events                map[string]string               `yaml:"events"`
	Actions               map[string]*metadata.FormAction `yaml:"actions"`
	Conditional           []rawFormCondRule               `yaml:"conditional"`
	ConditionalFormatting []rawFormCondRule               `yaml:"conditional_formatting"`
	OneCMeta              map[string]any                  `yaml:"oneC_meta"`
}

type rawFormCondRule struct {
	When      string                 `yaml:"when"`
	Target    string                 `yaml:"target"`
	Element   string                 `yaml:"element"`
	TablePart string                 `yaml:"table_part"`
	Field     string                 `yaml:"field"`
	Style     metadata.FormCellStyle `yaml:"style"`
	Then      metadata.FormCellStyle `yaml:"then"`
}

func (mfl *ManagedFormLoader) parseYAML(data []byte, entityNameFallback string) (*metadata.FormModule, error) {
	var doc formYAMLDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if doc.Schema != "" && doc.Schema != "onebase.form/v1" {
		return nil, i18nerr.Errorf("unsupported form schema %q (ожидается onebase.form/v1)", doc.Schema)
	}

	entity := doc.Form.Entity
	if entity == "" {
		entity = entityNameFallback
	}
	if entity == "" {
		return nil, i18nerr.New("form.entity не указан и нет fallback")
	}

	form := &metadata.FormModule{
		EntityName:             entity,
		Name:                   doc.Form.Name,
		Kind:                   doc.Form.Kind,
		LayoutKind:             metadata.FormLayoutManaged,
		Title:                  doc.Form.Title,
		OriginalID:             doc.Form.OriginalID,
		AutoSaveDataInSettings: doc.Form.AutoSaveDataInSettings,
		VerticalScroll:         doc.Form.VerticalScroll,
		Attributes:             doc.Attributes,
		Commands:               doc.Commands,
		AutoCommandBar:         doc.CommandBar,
		Elements:               doc.Elements,
		Handlers:               toEventMap(doc.Events),
		Actions:                doc.Actions,
		Conditional:            rawFormConditional(doc),
		Procedures:             make(map[string]*metadata.FormProcedure),
		OneCMeta:               doc.OneCMeta,
	}

	if form.Name == "" {
		return nil, i18nerr.New("form.name пустой")
	}
	if form.Kind == "" {
		form.Kind = "custom"
	}

	return form, nil
}

func rawFormConditional(doc formYAMLDoc) []metadata.FormCondRule {
	rawRules := append([]rawFormCondRule{}, doc.Conditional...)
	rawRules = append(rawRules, doc.ConditionalFormatting...)
	out := make([]metadata.FormCondRule, 0, len(rawRules))
	for _, rr := range rawRules {
		style := rr.Style
		if formStyleZero(style) {
			style = rr.Then
		}
		target := rr.Target
		if target == "" {
			target = rr.Element
		}
		if target == "" {
			target = rr.TablePart
		}
		out = append(out, metadata.FormCondRule{
			When:   rr.When,
			Target: target,
			Field:  rr.Field,
			Style:  style,
		})
	}
	return out
}

func formStyleZero(s metadata.FormCellStyle) bool {
	return s.Color == "" && s.Background == "" && !s.Bold && !s.Italic
}

// attachProcedures парсит .form.os и наполняет form.Procedures / form.Handlers.
// Использует существующую логику FormLoader.LoadFormModuleFromSource — но
// мерджит результат в уже разобранную managed-форму, не подменяя
// декларативные поля (Elements/Attributes/Commands).
func (mfl *ManagedFormLoader) attachProcedures(form *metadata.FormModule, osPath string) error {
	source, err := os.ReadFile(osPath)
	if err != nil {
		return err
	}
	parsed, err := mfl.innerFL.LoadFormModuleFromSource(string(source), form.EntityName, form.Name, form.Kind)
	if err != nil {
		return err
	}
	// процедуры — копируем целиком
	for name, proc := range parsed.Procedures {
		form.Procedures[name] = proc
	}
	// form-level handlers, найденные по имени процедуры, дополняют
	// то что было задано декларативно в YAML (YAML имеет приоритет).
	for evt, proc := range parsed.Handlers {
		if _, ok := form.Handlers[evt]; !ok {
			if form.Handlers == nil {
				form.Handlers = make(map[metadata.FormEventType]string)
			}
			form.Handlers[evt] = proc
		}
	}
	// AST модуля — нужен рантайму событий формы (этап 8 плана 37) для
	// извлечения *ast.ProcedureDecl по имени и запуска через interp.Run.
	if parsed.ProgramAST != nil {
		form.ProgramAST = parsed.ProgramAST
	}
	return nil
}

// toEventMap приводит map[string]string из YAML к map[FormEventType]string.
func toEventMap(in map[string]string) map[metadata.FormEventType]string {
	if len(in) == 0 {
		return make(map[metadata.FormEventType]string)
	}
	out := make(map[metadata.FormEventType]string, len(in))
	for k, v := range in {
		out[metadata.FormEventType(k)] = v
	}
	return out
}

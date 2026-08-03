package onec_forms

import (
	"errors"
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotImplemented возвращается фасадными функциями до реализации
// соответствующих этапов плана 37 (этап 5 — экспорт, этап 6 — Validate).
var ErrNotImplemented = errors.New("onec_forms: not implemented yet (см. План 37)")

// ImportOptions задаёт пути и метаданные для фасада ImportFromOneC.
type ImportOptions struct {
	// XMLPath — путь к Form.xml (обязателен).
	XMLPath string
	// BSLPath — путь к Module.bsl. Если файл отсутствует, импорт продолжится без модуля.
	BSLPath string
	// ItemsDir — путь к папке Items/ с бинарными ресурсами. Может быть пустым/отсутствовать.
	ItemsDir string

	// EntityName — имя сущности OneBase, к которой привязывается форма.
	EntityName string
	// FormName — имя формы (по умолчанию вытаскивается из имени каталога или ставится "Форма").
	FormName string
	// FormKind — object|list|choice|folder|custom (по умолчанию "custom").
	FormKind string

	// DstYAMLPath — путь к создаваемому .form.yaml.
	DstYAMLPath string
	// DstOSPath — путь к создаваемому .form.os (рядом с YAML).
	DstOSPath string
	// DstResourcesDir — каталог для _resources/.
	DstResourcesDir string
}

// ImportFromOneC читает форму из выгрузки 1С (Form.xml + Module.bsl + Items/*)
// и записывает её в проект OneBase как .form.yaml + .form.os + _resources/.
//
// Возвращает ImportReport с путями созданных файлов и списком предупреждений
// от парсера XML, нормализации, BSL-лексера и копирования ресурсов.
func ImportFromOneC(opts ImportOptions) (*ImportReport, error) {
	if opts.XMLPath == "" {
		return nil, fmt.Errorf("ImportFromOneC: XMLPath обязателен")
	}
	if opts.DstYAMLPath == "" {
		return nil, fmt.Errorf("ImportFromOneC: DstYAMLPath обязателен")
	}

	report := &ImportReport{}

	// 1. Парсим Form.xml.
	form, xmlWarns, err := ReadFormXML(opts.XMLPath)
	if err != nil {
		return nil, fmt.Errorf("read xml: %w", err)
	}
	report.Warnings = append(report.Warnings, xmlWarns...)

	// 2. Нормализация имён 1С → OneBase.
	normWarns := NormalizeForImport(form)
	report.Warnings = append(report.Warnings, normWarns...)

	// 3. Метаданные формы (entity/name/kind заполняются опциями).
	form.Entity = opts.EntityName
	if opts.FormName != "" {
		form.Name = opts.FormName
	} else if form.Name == "" {
		form.Name = "Форма"
	}
	if opts.FormKind != "" {
		form.Kind = opts.FormKind
	} else if form.Kind == "" {
		form.Kind = "custom"
	}

	// 4. Бинарные ресурсы (если есть Items/).
	if opts.ItemsDir != "" && opts.DstResourcesDir != "" {
		resources, resWarns, err := CopyResources(opts.ItemsDir, opts.DstResourcesDir)
		if err != nil {
			return report, fmt.Errorf("copy resources: %w", err)
		}
		report.Warnings = append(report.Warnings, resWarns...)
		AttachResourcesToForm(form, resources)
		if len(resources) > 0 {
			report.ResourcesDir = opts.DstResourcesDir
		}
	}

	// 5. Записываем YAML.
	if err := os.MkdirAll(filepath.Dir(opts.DstYAMLPath), fsmode.Dir); err != nil {
		return report, fmt.Errorf("mkdir for yaml: %w", err)
	}
	if err := WriteFormYAML(form, opts.DstYAMLPath); err != nil {
		return report, fmt.Errorf("write yaml: %w", err)
	}
	report.YAMLPath = opts.DstYAMLPath

	// 6. Module.bsl → .form.os.
	if opts.BSLPath != "" && opts.DstOSPath != "" {
		procs, bslWarns, err := ReadBSL(opts.BSLPath)
		if err != nil {
			return report, fmt.Errorf("read bsl: %w", err)
		}
		report.Warnings = append(report.Warnings, bslWarns...)
		if len(procs) > 0 {
			dsl := EmitDSLSource(procs)
			if err := os.MkdirAll(filepath.Dir(opts.DstOSPath), fsmode.Dir); err != nil {
				return report, fmt.Errorf("mkdir for os: %w", err)
			}
			if err := WriteFormOS(dsl, opts.DstOSPath); err != nil {
				return report, fmt.Errorf("write os: %w", err)
			}
			report.ModulePath = opts.DstOSPath
		}
	}

	return report, nil
}

// ExportOptions задаёт пути для фасада ExportToOneC.
type ExportOptions struct {
	// YAMLPath — путь к .form.yaml (обязателен).
	YAMLPath string
	// OSPath — путь к .form.os (опционально). Без него Module.bsl не создаётся.
	OSPath string
	// ResourcesDir — каталог с бинарными ресурсами проекта (опционально).
	ResourcesDir string

	// DstFormDir — каталог куда писать Form.xml + Form/Module.bsl + Form/Items/.
	// Обычно <1c-config>/Forms/<FormName>/Ext.
	DstFormDir string
}

// ExportToOneC обратное направление: читает .form.yaml + .form.os из проекта
// OneBase и пишет Form.xml + Module.bsl + Items/* в указанный каталог.
//
// Каталог должен соответствовать пути «Forms/<FormName>/Ext» в выгрузке 1С.
// Внутри создаются:
//
//	<DstFormDir>/Form.xml
//	<DstFormDir>/Form/Module.bsl    (если был OSPath)
//	<DstFormDir>/Form/Items/<X>/…   (если был ResourcesDir с файлами)
func ExportToOneC(opts ExportOptions) (*ExportReport, error) {
	if opts.YAMLPath == "" {
		return nil, fmt.Errorf("ExportToOneC: YAMLPath обязателен")
	}
	if opts.DstFormDir == "" {
		return nil, fmt.Errorf("ExportToOneC: DstFormDir обязателен")
	}

	report := &ExportReport{}

	// 1. YAML → IR
	form, err := ReadFormYAML(opts.YAMLPath)
	if err != nil {
		return nil, fmt.Errorf("read yaml: %w", err)
	}

	// 2. Нормализация IR в 1С-канон.
	report.Warnings = append(report.Warnings, NormalizeForExport(form)...)

	// 3. Подготовить каталог.
	if err := os.MkdirAll(opts.DstFormDir, fsmode.Dir); err != nil {
		return report, err
	}

	// 4. Form.xml.
	xmlPath := filepath.Join(opts.DstFormDir, "Form.xml")
	if err := WriteFormXML(form, xmlPath); err != nil {
		return report, fmt.Errorf("write xml: %w", err)
	}

	// 5. Module.bsl (если .form.os был задан).
	if opts.OSPath != "" {
		if dsl, err := os.ReadFile(opts.OSPath); err == nil {
			bslSource, bslWarns := EmitBSLFromDSL(string(dsl))
			report.Warnings = append(report.Warnings, bslWarns...)
			bslPath := filepath.Join(opts.DstFormDir, "Form", "Module.bsl")
			if err := os.MkdirAll(filepath.Dir(bslPath), fsmode.Dir); err != nil {
				return report, err
			}
			if err := os.WriteFile(bslPath, []byte(bslSource), fsmode.File); err != nil {
				return report, fmt.Errorf("write bsl: %w", err)
			}
		}
		// если файла нет — это нормально, не все формы имеют модуль
	}

	// 6. Items/ из ResourcesDir (если задан).
	if opts.ResourcesDir != "" {
		if _, err := os.Stat(opts.ResourcesDir); err == nil {
			itemsDir := filepath.Join(opts.DstFormDir, "Form", "Items")
			if err := exportResources(opts.ResourcesDir, itemsDir); err != nil {
				report.Warnings = append(report.Warnings, Warning{
					Severity: SeverityWarn, Code: W013_ResourceMissing,
					Message: "не удалось перенести ресурсы: " + err.Error(),
				})
			}
		}
	}

	report.FormDir = opts.DstFormDir
	return report, nil
}

// exportResources копирует подкаталоги из <_resources> в <Items>/<X>/...
func exportResources(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, fsmode.Dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		elDir := filepath.Join(srcDir, e.Name())
		dstElDir := filepath.Join(dstDir, e.Name())
		if err := os.MkdirAll(dstElDir, fsmode.Dir); err != nil {
			return err
		}
		files, err := os.ReadDir(elDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			src := filepath.Join(elDir, f.Name())
			dst := filepath.Join(dstElDir, f.Name())
			if err := copyFile(src, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate проверяет корректность .form.yaml: схема, тип FormElement.Kind,
// data_path (обязателен у полей ввода и чекбоксов), уникальность имён в дереве.
// Возвращает список предупреждений (всех уровней). Если есть error-warnings —
// форма не считается валидной.
//
// Этого достаточно для CLI/UI проверки «без запуска проекта». Полноценная
// валидация data_path против реального metadata.Entity делается отдельно
// (handler /forms/validate в этапе 4 умеет только базовый YAML-парсинг).
func Validate(yamlPath string) ([]Warning, error) {
	form, err := ReadFormYAML(yamlPath)
	if err != nil {
		return []Warning{{Severity: SeverityError, Code: W003_InvalidYAML, Message: err.Error()}}, nil
	}

	var warns Warnings

	// 1. Имена и Kind элементов: пустые имена, неизвестные Kind.
	seenNames := map[string]int{}
	var walk func(*IRElement, []string)
	walk = func(el *IRElement, path []string) {
		if el == nil {
			return
		}
		full := append(path, el.Name)
		if el.Name == "" {
			warns.Add(Warning{Severity: SeverityWarn, Code: W050_NeedsReview, Element: strings.Join(full, "/"), Message: "элемент без имени"})
		}
		if el.Kind == "" {
			warns.Add(Warning{Severity: SeverityError, Code: W010_UnknownElement, Element: el.Name, Message: "не указан kind"})
		} else if !knownKind(el.Kind) {
			warns.Add(Warning{Severity: SeverityWarn, Code: W010_UnknownElement, Element: el.Name, Field: el.Kind, Message: "неизвестный kind"})
		}
		if el.Name != "" {
			seenNames[el.Name]++
			if seenNames[el.Name] == 2 {
				warns.Add(Warning{Severity: SeverityWarn, Code: W050_NeedsReview, Element: el.Name, Message: "имя встречается у нескольких элементов формы"})
			}
		}
		// data_path обязателен для полей ввода и флажков.
		if requiresDataPath(el.Kind) && el.DataPath == "" {
			warns.Add(Warning{Severity: SeverityError, Code: W012_MissingDataPath, Element: el.Name, Field: el.Kind, Message: "data_path обязателен для этого типа элемента"})
		}
		for _, c := range el.Children {
			walk(c, full)
		}
	}
	for _, el := range form.Elements {
		walk(el, nil)
	}

	// 2. Реквизиты: непустые имена и типы.
	for i, a := range form.Attributes {
		if a.Name == "" {
			warns.Add(Warning{Severity: SeverityError, Code: W050_NeedsReview, Field: "attributes", Message: fmt.Sprintf("реквизит[%d] без имени", i)})
		}
		if a.TypeRef == "" {
			warns.Add(Warning{Severity: SeverityError, Code: W022_UnknownType, Element: a.Name, Message: "не указан type"})
		}
	}
	for _, a := range form.Attributes {
		if a.TypeRef == "ValueTable" && len(a.Columns) == 0 {
			warns.Add(Warning{Severity: SeverityInfo, Code: W050_NeedsReview, Element: a.Name, Message: "ValueTable без колонок"})
		}
	}

	// 3. Команда без action. Управляемая форма рисует команды из commands:
	// автоматической командной панелью, а элемент kind: Кнопка нужен лишь чтобы
	// поставить команду в конкретное место. Но панель берёт только команды с
	// непустым action (ui.unplacedCommands): команда без action не отрисуется
	// нигде и её нечем вызвать — молча пропадает из интерфейса.
	for _, c := range form.Commands {
		if c == nil || strings.TrimSpace(c.Action) != "" {
			continue
		}
		warns.Add(Warning{Severity: SeverityWarn, Code: W014_CommandNoAction, Element: c.Name, Field: "commands",
			Message: fmt.Sprintf("у команды «%s» не задан action — она не попадёт ни в командную панель, ни на кнопку", c.Name),
			Suggest: "укажите action: <ИмяПроцедуры> и объявите эту процедуру в одноимённом .form.os"})
	}

	// 4. Реквизит формы перечислимого типа (save:false), показанный через
	// ПолеВвода, НЕ получит выбор. Ссылочные реквизиты (CatalogRef./DocumentRef.)
	// пикер получают — рендер грузит для них варианты справочника
	// (ui.mergeFormLocalRefOptions), поэтому здесь проверяются только типы,
	// которые этим путём не покрыты: перечисления, план счетов, AnyRef.
	localRef := map[string]string{}
	for _, a := range form.Attributes {
		if !a.Save && isUnpickableFormLocalType(a.TypeRef) {
			localRef[a.Name] = a.TypeRef
		}
	}
	if len(localRef) > 0 {
		var walkFields func(*IRElement)
		walkFields = func(el *IRElement) {
			if el == nil {
				return
			}
			// form-local data_path — простое имя реквизита без префикса «Объект.»/«Список.».
			if el.Kind == "ПолеВвода" && el.DataPath != "" && !strings.Contains(el.DataPath, ".") {
				if tr, ok := localRef[el.DataPath]; ok {
					warns.Add(Warning{Severity: SeverityWarn, Code: W015_FormLocalRefField, Element: el.Name, Field: el.DataPath,
						Message: fmt.Sprintf("поле привязано к реквизиту формы «%s» типа «%s» (save:false) — выбор значения не отрисуется", el.DataPath, tr),
						Suggest: "используйте поле объекта (data_path: Объект.<поле>) — для перечислений и плана счетов выбор даётся только полям сущности; у реквизита формы выбор из справочника работает лишь для CatalogRef./DocumentRef."})
				}
			}
			for _, c := range el.Children {
				walkFields(c)
			}
		}
		for _, el := range form.Elements {
			walkFields(el)
		}
	}

	return []Warning(warns), nil
}

// isUnpickableFormLocalType сообщает, что у реквизита формы такого типа выбор
// значения не отрисуется. CatalogRef./DocumentRef. сюда НЕ входят: для них
// ui.mergeFormLocalRefOptions грузит варианты справочника и поле получает
// полноценный пикер. Остаются перечисления, план счетов и AnyRef — выбор для
// них рисуется только у полей сущности.
func isUnpickableFormLocalType(t string) bool {
	for _, p := range []string{"EnumRef.", "ChartOfAccountsRef.", "AnyRef"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return strings.HasPrefix(t, "enum:") || strings.HasPrefix(t, "Перечисление")
}

// knownKind возвращает true если el.Kind — известный нам тип элемента
// (любой OneBase-канон или 1С-имя из elements_map).
func knownKind(kind string) bool {
	if _, ok := Element1CToOneBase(kind); ok {
		return true
	}
	// OneBase-имя — ищем как значение в карте.
	for _, v := range elementMap {
		if string(v) == kind {
			return true
		}
	}
	// Дополнительные OneBase-Kinds, не имеющие прямого 1С-аналога.
	switch kind {
	case "ПолеВвода", "Надпись", "Кнопка", "Таблица", "ГруппаФормы",
		"Страница", "СтраницыФормы", "Флажок", "Переключатель",
		"ПолеСписка", "ПолеДаты", "ПолеФормы", "ТабличнаяЧасть",
		"Колонка", "КоманднаяПанель", "ПолеКартинки", "КнопкаКП":
		return true
	}
	return false
}

// requiresDataPath возвращает true для элементов, у которых отсутствие
// data_path — это ошибка (нельзя привязать поле к данным).
func requiresDataPath(kind string) bool {
	switch kind {
	case "ПолеВвода", "Флажок", "Переключатель", "ПолеДаты", "ПолеСписка",
		"InputField", "CheckBoxField", "RadioButtonField", "PictureField":
		return true
	}
	return false
}

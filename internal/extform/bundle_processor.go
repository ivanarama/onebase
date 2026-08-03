package extform

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/processor"
	"gopkg.in/yaml.v3"
)

// Формат внешней обработки — единый YAML: метаданные обработки (name, title,
// params, table_parts) плюс поле code с исходником .proc.os. Пример:
//
//	name: МояОбработка
//	params:
//	  - { name: Режим, type: string }
//	code: |
//	  // Обработка
//	  Процедура Выполнить()
//	    Сообщить("привет");
//	  КонецПроцедуры
//
// При импорте может прийти в обёртке бандла *.obform (manifest + form).

// ParsedProcessor — результат разбора загруженной обработки, готовый к сохранению.
type ParsedProcessor struct {
	Name        string
	Content     []byte // единый YAML (метаданные + code)
	Author      string
	Version     string
	MinPlatform string
}

// ParseProcessorUpload разбирает загруженную обработку: «голый» YAML (метаданные
// + code) либо бандл *.obform с секциями manifest/form (kind: processor).
func ParseProcessorUpload(data []byte) (*ParsedProcessor, error) {
	var bd struct {
		Manifest *Manifest `yaml:"manifest"`
		Form     yaml.Node `yaml:"form"`
	}
	_ = yaml.Unmarshal(data, &bd)

	p := &ParsedProcessor{}
	hasForm := bd.Form.Kind != 0
	if bd.Manifest != nil || hasForm {
		if !hasForm {
			return nil, fmt.Errorf("бандл без секции form")
		}
		node := &bd.Form
		if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
			node = node.Content[0]
		}
		content, err := yaml.Marshal(node)
		if err != nil {
			return nil, fmt.Errorf("сериализация form: %w", err)
		}
		p.Content = content
		if bd.Manifest != nil {
			p.Name = bd.Manifest.Name
			p.Author = bd.Manifest.Author
			p.Version = bd.Manifest.Version
			p.MinPlatform = bd.Manifest.MinPlatform
		}
	} else {
		p.Content = data
	}

	// Полная валидация: метаданные, наличие code, разбор кода, наличие Выполнить.
	proc, _, err := ParseProcessorContent(p.Content)
	if err != nil {
		return nil, err
	}
	if proc.Name != "" {
		p.Name = proc.Name
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("не указано имя обработки (поле name)")
	}
	return p, nil
}

// ParseProcessorContent разбирает единый YAML обработки: метаданные + исходный
// код. Проверяет, что код присутствует, компилируется и содержит процедуру
// Выполнить(). Используется и при загрузке в реестр, и при валидации upload.
func ParseProcessorContent(content []byte) (*processor.Processor, *ast.Program, error) {
	proc, err := processor.ParseBytes(content)
	if err != nil {
		return nil, nil, fmt.Errorf("некорректный YAML обработки: %w", err)
	}
	var codeHolder struct {
		Code string `yaml:"code"`
	}
	if err := yaml.Unmarshal(content, &codeHolder); err != nil {
		return nil, nil, fmt.Errorf("чтение поля code: %w", err)
	}
	if strings.TrimSpace(codeHolder.Code) == "" {
		return nil, nil, fmt.Errorf("у обработки пустой код (поле code)")
	}
	prog, err := parser.New(lexer.New(codeHolder.Code, proc.Name+".proc.os")).ParseProgram()
	if err != nil {
		return nil, nil, fmt.Errorf("код не компилируется: %w", err)
	}
	if !hasProc(prog, "Выполнить", "Execute") {
		return nil, nil, fmt.Errorf("в коде нет процедуры Выполнить()")
	}
	return proc, prog, nil
}

func hasProc(prog *ast.Program, names ...string) bool {
	for _, d := range prog.Procedures {
		for _, n := range names {
			if strings.EqualFold(d.Name.Literal, n) {
				return true
			}
		}
	}
	return false
}

// BuildProcessorBundle собирает переносимый бандл *.obform из записи обработки.
func BuildProcessorBundle(rec *ProcessorRecord, platformVersion string) ([]byte, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(rec.Content, &node); err != nil {
		return nil, fmt.Errorf("разбор содержимого обработки: %w", err)
	}
	n := &node
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	out := struct {
		Manifest Manifest   `yaml:"manifest"`
		Form     *yaml.Node `yaml:"form"`
	}{
		Manifest: Manifest{
			Kind:        "processor",
			Name:        rec.Name,
			Author:      rec.Author,
			Version:     rec.Version,
			MinPlatform: platformVersion,
		},
		Form: n,
	}
	return yaml.Marshal(out)
}

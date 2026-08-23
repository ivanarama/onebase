package processor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"gopkg.in/yaml.v3"
)

type Param struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`    // string, text, number, date, bool, choice, reference:Entity
	Label   string            `yaml:"label"`   // подпись поля; по умолчанию совпадает с Name
	Labels  map[string]string `yaml:"labels"`  // переводы подписи по языкам (lang code → перевод)
	Default any               `yaml:"default"` // значение по умолчанию
	Options []string          `yaml:"options"` // для type: choice — список вариантов выбора

	// Required помечает параметр обязательным: форма не отправится пустой, а
	// сервер откажет до запуска. Нужен прежде всего файловым параметрам —
	// браузер не может вернуть выбранный файл в поле после перезагрузки
	// страницы, поэтому повторное «Выполнить» уходило на сервер с пустым файлом,
	// и человек получал ошибку прикладного модуля вместо подсказки формы.
	Required bool `yaml:"required"`
}

type Processor struct {
	Name   string            `yaml:"name"`
	Title  string            `yaml:"title"`
	Titles map[string]string `yaml:"titles"`
	// Kind помечает роль обработки. Пусто — обычная обработка. "test" — тест
	// уровня конфигурации: попадает в дискавери `onebase test` (план 136) и
	// прячется из общего списка обработок пользователя.
	Kind       string                 `yaml:"kind"`
	Params     []Param                `yaml:"params"`
	TableParts []metadata.TablePart   `yaml:"table_parts"`
	Forms      []*metadata.FormModule `yaml:"-"`

	// External помечает обработку из внешнего контура (таблица
	// _ext_processors), а не из конфигурации проекта. Trusted — признак того,
	// что администратор пометил внешнюю обработку доверенной: тогда её видят и
	// запускают обычные пользователи, иначе — только администратор. Оба поля
	// заполняются программно при загрузке; в YAML не сериализуются.
	External bool `yaml:"-"`
	Trusted  bool `yaml:"-"`

	// Layout — макет-заготовка обработки из src/<имя>.proc.layout.yaml (если
	// файл есть рядом с .proc.os). Заполняется программно при загрузке проекта
	// (см. project.loadProcessorLayouts) и инжектируется в DSL как переменная
	// «Макет» во всех путях запуска обработки. В YAML не сериализуется.
	Layout *printform.LayoutTemplate `yaml:"-"`
}

// IsTest сообщает, что обработка — тест уровня конфигурации (kind: test).
func (p *Processor) IsTest() bool {
	return strings.EqualFold(p.Kind, "test")
}

// DisplayLabel возвращает подпись параметра с учётом языка.
func (p Param) DisplayLabel(lang string) string {
	if lang != "" {
		if v, ok := p.Labels[lang]; ok && v != "" {
			return v
		}
	}
	if p.Label != "" {
		return p.Label
	}
	return p.Name
}

// DisplayName возвращает заголовок обработки с учётом языка.
func (p *Processor) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := p.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if p.Title != "" {
		return p.Title
	}
	return p.Name
}

// ManagedForm возвращает первую managed-форму обработки или nil.
func (p *Processor) ManagedForm() *metadata.FormModule {
	for _, f := range p.Forms {
		if f != nil && f.IsManaged() {
			return f
		}
	}
	return nil
}

func LoadFile(path string) (*Processor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data)
}

// ParseBytes разбирает YAML метаданных обработки из памяти (без файла). Нужна
// для внешних обработок, хранящихся в БД (см. internal/extform). Поле code в
// YAML (исходник .proc.os для внешних обработок) при этом игнорируется —
// извлекается отдельно вызывающим.
func ParseBytes(data []byte) (*Processor, error) {
	var proc Processor
	if err := yaml.Unmarshal(data, &proc); err != nil {
		return nil, err
	}
	return &proc, nil
}

func LoadDir(dir string) ([]*Processor, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var procs []*Processor
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		proc, err := LoadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		procs = append(procs, proc)
	}
	return procs, nil
}

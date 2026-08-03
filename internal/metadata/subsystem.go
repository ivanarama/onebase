package metadata

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Subsystem struct {
	Name   string
	Title  string
	Titles map[string]string
	Icon   string
	Order  int
	// Roles — необязательный whitelist ролей (как у страниц и HTTP-сервисов):
	// непустой список показывает раздел только пользователям с одной из ролей
	// (админ видит всегда). Пустой — видимость определяется правами на объекты.
	Roles    []string
	Contents SubsystemContents
	HomePage *HomePage
}

// DisplayName возвращает заголовок подсистемы с учётом языка.
func (s *Subsystem) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := s.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}

type SubsystemContents struct {
	Documents  []string
	Catalogs   []string
	Reports    []string
	InfoRegs   []string
	Registers  []string
	Processors []string
	Journals   []string
	Pages      []string // план 66: произвольные страницы на DSL
}

// IsEmpty сообщает, что набор объектов пуст (ни одного объекта ни в одной
// категории). Используется, чтобы отличить заданный, но пустой nav от
// отсутствующего.
func (c *SubsystemContents) IsEmpty() bool {
	if c == nil {
		return true
	}
	return len(c.Documents) == 0 && len(c.Catalogs) == 0 && len(c.Reports) == 0 &&
		len(c.InfoRegs) == 0 && len(c.Registers) == 0 && len(c.Processors) == 0 &&
		len(c.Journals) == 0 && len(c.Pages) == 0
}

type rawSubsystem struct {
	Name     string            `yaml:"name"`
	Title    string            `yaml:"title"`
	Titles   map[string]string `yaml:"titles"`
	Icon     string            `yaml:"icon"`
	Order    int               `yaml:"order"`
	Roles    []string          `yaml:"roles"`
	Contents struct {
		Documents  []string `yaml:"documents"`
		Catalogs   []string `yaml:"catalogs"`
		Reports    []string `yaml:"reports"`
		InfoRegs   []string `yaml:"inforegs"`
		Registers  []string `yaml:"registers"`
		Processors []string `yaml:"processors"`
		Journals   []string `yaml:"journals"`
		Pages      []string `yaml:"pages"`
	} `yaml:"contents"`
	HomePage *HomePage `yaml:"home_page"`
}

func LoadSubsystemFile(path string) (*Subsystem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawSubsystem
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Name == "" {
		raw.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
	}
	if raw.Title == "" {
		raw.Title = raw.Name
	}
	ss := &Subsystem{
		Name:   raw.Name,
		Title:  raw.Title,
		Titles: raw.Titles,
		Icon:   raw.Icon,
		Order:  raw.Order,
		Roles:  raw.Roles,
		Contents: SubsystemContents{
			Documents:  raw.Contents.Documents,
			Catalogs:   raw.Contents.Catalogs,
			Reports:    raw.Contents.Reports,
			InfoRegs:   raw.Contents.InfoRegs,
			Registers:  raw.Contents.Registers,
			Processors: raw.Contents.Processors,
			Journals:   raw.Contents.Journals,
			Pages:      raw.Contents.Pages,
		},
	}
	if raw.HomePage != nil {
		raw.HomePage.applyDefaults()
		ss.HomePage = raw.HomePage
	}
	return ss, nil
}

func LoadSubsystemDir(dir string) ([]*Subsystem, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var subs []*Subsystem
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		s, err := LoadSubsystemFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].Order != subs[j].Order {
			return subs[i].Order < subs[j].Order
		}
		return subs[i].Name < subs[j].Name
	})
	return subs, nil
}

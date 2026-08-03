package extform

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/printform"
	"gopkg.in/yaml.v3"
)

// Manifest — обёртка переносимого бандла *.obform: метаданные формы для
// дистрибуции между базами (задел под маркетплейс).
type Manifest struct {
	Kind        string `yaml:"kind"` // "printform" или "report"
	Name        string `yaml:"name"`
	Document    string `yaml:"document,omitempty"` // только для печатных форм
	Author      string `yaml:"author,omitempty"`
	Version     string `yaml:"version,omitempty"`
	MinPlatform string `yaml:"min_platform,omitempty"`
}

// Parsed — результат разбора загруженного файла, готовый к сохранению.
type Parsed struct {
	Name        string
	Document    string
	Content     []byte // «голый» YAML printform.PrintForm (без обёртки manifest/form)
	Author      string
	Version     string
	MinPlatform string
}

// ParseUpload разбирает загруженный администратором файл. Принимает два
// формата:
//   - бандл *.obform с секциями manifest/form (для переноса между базами);
//   - «голый» YAML печатной формы (как в printforms/*.yaml).
//
// В обоих случаях возвращает Content — «голый» YAML формы, который при загрузке
// конвертируется в макет v2 (ConvertLegacy) и рендерится декларативным движком.
// Имя и документ берутся из самой формы, с откатом на manifest.
func ParseUpload(data []byte) (*Parsed, error) {
	var bd struct {
		Manifest *Manifest `yaml:"manifest"`
		Form     yaml.Node `yaml:"form"`
	}
	// Ошибку игнорируем: «голый» PrintForm-YAML не содержит manifest/form и
	// просто даст нулевые поля — это валидный случай, разбираем его ниже.
	_ = yaml.Unmarshal(data, &bd)

	p := &Parsed{}
	hasForm := bd.Form.Kind != 0
	if bd.Manifest != nil || hasForm {
		// Бандл.
		if !hasForm {
			return nil, fmt.Errorf("бандл без секции form")
		}
		formNode := &bd.Form
		if formNode.Kind == yaml.DocumentNode && len(formNode.Content) == 1 {
			formNode = formNode.Content[0]
		}
		content, err := yaml.Marshal(formNode)
		if err != nil {
			return nil, fmt.Errorf("сериализация form: %w", err)
		}
		p.Content = content
		if bd.Manifest != nil {
			p.Name = bd.Manifest.Name
			p.Document = bd.Manifest.Document
			p.Author = bd.Manifest.Author
			p.Version = bd.Manifest.Version
			p.MinPlatform = bd.Manifest.MinPlatform
		}
	} else {
		p.Content = data
	}

	// Валидация тела формы + дозаполнение имени/документа из неё.
	pf, err := printform.ParseBytes(p.Content)
	if err != nil {
		return nil, fmt.Errorf("некорректный YAML печатной формы: %w", err)
	}
	if pf.Name != "" {
		p.Name = pf.Name
	}
	if pf.Document != "" {
		p.Document = pf.Document
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("не указано имя формы (поле name)")
	}
	if strings.TrimSpace(p.Document) == "" {
		return nil, fmt.Errorf("не указан документ формы (поле document)")
	}
	return p, nil
}

// BuildBundle собирает переносимый бандл *.obform из записи. min_platform
// проставляется текущей версией платформы — на другой базе она проверяется
// при импорте (CheckMinPlatform).
func BuildBundle(rec *Record, platformVersion string) ([]byte, error) {
	var formNode yaml.Node
	if err := yaml.Unmarshal(rec.Content, &formNode); err != nil {
		return nil, fmt.Errorf("разбор содержимого формы: %w", err)
	}
	node := &formNode
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	out := struct {
		Manifest Manifest   `yaml:"manifest"`
		Form     *yaml.Node `yaml:"form"`
	}{
		Manifest: Manifest{
			Kind:        "printform",
			Name:        rec.Name,
			Document:    rec.Document,
			Author:      rec.Author,
			Version:     rec.Version,
			MinPlatform: platformVersion,
		},
		Form: node,
	}
	return yaml.Marshal(out)
}

// CheckMinPlatform возвращает ошибку, если требуемая бандлом версия платформы
// выше текущей. Best-effort: если хотя бы одна версия не парсится как набор
// числовых сегментов, проверка пропускается (не блокирует импорт).
func CheckMinPlatform(min, current string) error {
	if strings.TrimSpace(min) == "" {
		return nil
	}
	cmp, ok := compareVersions(current, min)
	if !ok {
		return nil
	}
	if cmp < 0 {
		return fmt.Errorf("форма требует версию платформы не ниже %s, текущая %s", min, current)
	}
	return nil
}

// compareVersions сравнивает две dotted-версии посегментно. Возвращает
// (-1|0|1, true) при успехе или (0, false), если что-то не распарсилось.
func compareVersions(a, b string) (int, bool) {
	as, ok := versionSegments(a)
	if !ok {
		return 0, false
	}
	bs, ok := versionSegments(b)
	if !ok {
		return 0, false
	}
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			if av < bv {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func versionSegments(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

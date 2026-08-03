package metadata

// ExchangePlan — план обмена (аналог «Плана обмена» 1С, план 86): состав
// синхронизируемых данных + узлы-участники + правило разрешения конфликтов.
// Загружается из exchange/<имя>.yaml.
//
// «Этот узел» в YAML НЕ хранится: конфигурация симметрична для всех баз (центр
// и филиал берут один и тот же exchange/*.yaml). Код текущего узла — база-
// специфичная настройка (_settings, ключ exchange.this_node), задаётся командой
// `onebase exchange init --plan X --node <код>`.

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Допустимые правила разрешения конфликтов версий при загрузке пакета.
const (
	ConflictByTime         = "by_time"          // побеждает позже изменённый объект
	ConflictByNodePriority = "by_node_priority" // побеждает узел с большим priority
	ConflictByHook         = "hook"             // выбор делает DSL-хук ПриКонфликтеОбмена
)

// ExchangePlan описывает один план обмена.
type ExchangePlan struct {
	Name     string            `yaml:"name"`
	Title    string            `yaml:"title"`
	Titles   map[string]string `yaml:"titles"`
	Content  []string          `yaml:"content"` // "Справочник.X" / "Документ.X" / "X"
	Nodes    []ExchangeNode    `yaml:"nodes"`
	Conflict string            `yaml:"conflict"` // by_time | by_node_priority | hook
	// Repost — перепроводить проведённые на источнике документы на приёмнике
	// (запустить ОбработкаПроведения, движения лягут в регистры приёмника). По
	// умолчанию false — документы приходят непроведёнными. Действует только на
	// онлайн-приёмнике (сервере); файловая CLI-загрузка оставляет непроведёнными.
	Repost bool `yaml:"repost"`

	// parsed — разобранный Content, заполняется в Normalize (при загрузке).
	// Кэшируется, потому что Includes зовётся на каждой записи объекта.
	parsed []ContentEntry
}

// Роли узла в топологии обмена (план 86, фаза 2). Пусто у всех узлов — плоская
// топология (каждый регистрирует изменения всем, прежнее поведение). Если есть
// хотя бы один hub — звезда: спицы обмениваются только с хабом, хаб ретранслирует
// изменения между спицами (аналог «распределённой ИБ» центр/периферия в 1С).
const (
	RoleHub   = "hub"
	RoleSpoke = "spoke"
)

// ExchangeNode — узел (участник) плана обмена.
type ExchangeNode struct {
	Code     string `yaml:"code"`
	Name     string `yaml:"name"`
	Priority int    `yaml:"priority"` // используется правилом by_node_priority
	// URL — сетевой адрес базы узла для онлайн-обмена (например
	// http://fil01:8080). Пусто — узел доступен только файловым обменом.
	// Не секрет; поддерживает ${env:VAR} (раскрывается загрузчиком).
	URL string `yaml:"url"`
	// Role — роль в топологии: "" (плоская), hub или spoke. См. RoleHub/RoleSpoke.
	Role string `yaml:"role"`
}

// ContentCategory — категория записи состава обмена: сущность (справочник/
// документ), константа или регистр сведений. Разделяет пространства имён: план
// может синхронизировать и справочник, и константу с одинаковым именем.
type ContentCategory int

const (
	ContentEntity       ContentCategory = iota // справочник или документ (см. Kind)
	ContentConstant                            // глобальная константа (Константа.X)
	ContentInfoRegister                        // регистр сведений (РегистрСведений.X)
)

// ContentEntry — разобранная запись состава обмена. Для ContentEntity поле Kind
// уточняет вид (Kind == "" — «любой вид с таким именем», запись без префикса
// Справочник./Документ.). Для констант и регистров сведений Kind не используется.
type ContentEntry struct {
	Category ContentCategory
	Kind     Kind
	Name     string
}

// DisplayName возвращает заголовок плана обмена с учётом языка.
func (p *ExchangePlan) DisplayName(lang string) string {
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

// Normalize приводит план к каноничному виду: тримует имена, дефолтит правило
// конфликта, разбирает состав. Вызывается загрузчиком; экспортирован для
// программного построения планов (в т.ч. в тестах — без него Includes всегда
// вернёт false).
func (p *ExchangePlan) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.Conflict = strings.ToLower(strings.TrimSpace(p.Conflict))
	if p.Conflict == "" {
		p.Conflict = ConflictByTime
	}
	for i := range p.Nodes {
		p.Nodes[i].Code = strings.TrimSpace(p.Nodes[i].Code)
		p.Nodes[i].Role = strings.ToLower(strings.TrimSpace(p.Nodes[i].Role))
	}
	p.parsed = p.parsed[:0]
	for _, c := range p.Content {
		if strings.TrimSpace(c) == "" {
			continue
		}
		p.parsed = append(p.parsed, parseContentEntry(c))
	}
}

// ParsedContent возвращает разобранный состав обмена (для валидации и движка).
func (p *ExchangePlan) ParsedContent() []ContentEntry {
	return p.parsed
}

// Includes сообщает, входит ли сущность в состав обмена. Сверяет имя
// регистронезависимо; если у записи состава указан вид (Справочник./Документ.),
// он должен совпасть с видом сущности.
func (p *ExchangePlan) Includes(e *Entity) bool {
	if e == nil {
		return false
	}
	for _, c := range p.parsed {
		if c.Category != ContentEntity || !strings.EqualFold(c.Name, e.Name) {
			continue
		}
		if c.Kind == "" || c.Kind == e.Kind {
			return true
		}
	}
	return false
}

// IncludesConstant сообщает, входит ли константа с таким именем в состав обмена.
func (p *ExchangePlan) IncludesConstant(name string) bool {
	return p.includesNamed(ContentConstant, name)
}

// IncludesInfoRegister сообщает, входит ли регистр сведений в состав обмена.
func (p *ExchangePlan) IncludesInfoRegister(name string) bool {
	return p.includesNamed(ContentInfoRegister, name)
}

func (p *ExchangePlan) includesNamed(cat ContentCategory, name string) bool {
	for _, c := range p.parsed {
		if c.Category == cat && strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

// Node ищет узел плана по коду (регистронезависимо). nil, если нет.
func (p *ExchangePlan) Node(code string) *ExchangeNode {
	for i := range p.Nodes {
		if strings.EqualFold(p.Nodes[i].Code, code) {
			return &p.Nodes[i]
		}
	}
	return nil
}

// HasHub сообщает, что план работает по топологии «звезда» (есть хотя бы один
// узел с ролью hub). Иначе топология плоская (прежнее поведение).
func (p *ExchangePlan) HasHub() bool {
	for i := range p.Nodes {
		if p.Nodes[i].Role == RoleHub {
			return true
		}
	}
	return false
}

// IsHub сообщает, что узел с этим кодом — хаб.
func (p *ExchangePlan) IsHub(code string) bool {
	n := p.Node(code)
	return n != nil && n.Role == RoleHub
}

// RegistrationTargets возвращает коды узлов, которым узел thisNode должен
// регистрировать свои изменения, с учётом топологии:
//   - плоская (нет хабов): все узлы, кроме себя (прежнее поведение);
//   - звезда, thisNode — хаб: все не-хабы (спицы);
//   - звезда, thisNode — спица: только хабы.
func (p *ExchangePlan) RegistrationTargets(thisNode string) []string {
	var out []string
	if !p.HasHub() {
		for i := range p.Nodes {
			if !strings.EqualFold(p.Nodes[i].Code, thisNode) {
				out = append(out, p.Nodes[i].Code)
			}
		}
		return out
	}
	if p.IsHub(thisNode) {
		for i := range p.Nodes {
			if strings.EqualFold(p.Nodes[i].Code, thisNode) || p.Nodes[i].Role == RoleHub {
				continue
			}
			out = append(out, p.Nodes[i].Code)
		}
		return out
	}
	for i := range p.Nodes {
		if p.Nodes[i].Role == RoleHub {
			out = append(out, p.Nodes[i].Code)
		}
	}
	return out
}

// TransitTargets возвращает спицы, которым хаб thisNode должен ретранслировать
// изменение, принятое от узла fromNode (все не-хабы, кроме источника). Пусто,
// если thisNode не хаб (спицы — листья, транзит не выполняют) — тогда обмен
// работает как раньше, без ретрансляции.
func (p *ExchangePlan) TransitTargets(thisNode, fromNode string) []string {
	if !p.IsHub(thisNode) {
		return nil
	}
	var out []string
	for i := range p.Nodes {
		n := &p.Nodes[i]
		if strings.EqualFold(n.Code, thisNode) || n.Role == RoleHub || strings.EqualFold(n.Code, fromNode) {
			continue
		}
		out = append(out, n.Code)
	}
	return out
}

// parseContentEntry разбирает запись состава: «Справочник.X»/«Документ.X»/«X»
// (сущность), «Константа.X» (константа), «РегистрСведений.X» (регистр сведений).
func parseContentEntry(s string) ContentEntry {
	s = strings.TrimSpace(s)
	// Категорийные префиксы (константа/регистр сведений) — до видовых, потому что
	// у них своё пространство имён.
	for _, pfx := range []struct {
		p   string
		cat ContentCategory
	}{
		{"константа.", ContentConstant},
		{"constant.", ContentConstant},
		{"регистрсведений.", ContentInfoRegister},
		{"inforegister.", ContentInfoRegister},
	} {
		if hasPrefixFold(s, pfx.p) {
			return ContentEntry{Category: pfx.cat, Name: strings.TrimSpace(s[len(pfx.p):])}
		}
	}
	for _, pfx := range []struct {
		p    string
		kind Kind
	}{
		{"справочник.", KindCatalog},
		{"catalog.", KindCatalog},
		{"документ.", KindDocument},
		{"document.", KindDocument},
	} {
		if hasPrefixFold(s, pfx.p) {
			return ContentEntry{Category: ContentEntity, Kind: pfx.kind, Name: strings.TrimSpace(s[len(pfx.p):])}
		}
	}
	return ContentEntry{Category: ContentEntity, Name: s}
}

// hasPrefixFold сравнивает префикс регистронезависимо. Для русских префиксов
// длина в байтах совпадает с оригиналом (пары регистра кириллицы одинаковой
// длины в UTF-8), поэтому срез по длине префикса далее безопасен.
func hasPrefixFold(s, pfx string) bool {
	return len(s) >= len(pfx) && strings.EqualFold(s[:len(pfx)], pfx)
}

// LoadExchangePlanFile читает один exchange/<имя>.yaml.
func LoadExchangePlanFile(path string) (*ExchangePlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p ExchangePlan
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Name == "" {
		p.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
	}
	p.Normalize()
	return &p, nil
}

// LoadExchangePlanDir читает все планы обмена из каталога exchange/.
// Отсутствие каталога — не ошибка (обмен не настроен).
func LoadExchangePlanDir(dir string) ([]*ExchangePlan, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plans []*ExchangePlan
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		p, err := LoadExchangePlanFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, nil
}

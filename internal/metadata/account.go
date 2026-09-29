package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Account represents a single entry in a chart of accounts.
type Account struct {
	Code   string            `yaml:"code"`
	Name   string            `yaml:"name"`
	Names  map[string]string `yaml:"names"`  // переводы имени счёта по языкам
	Kind   string            `yaml:"kind"`   // active | passive | active_passive
	Parent string            `yaml:"parent"` // parent code for hierarchy
	// Children — вложенная форма той же иерархии (`children:` в YAML, как в
	// DEVELOPER.md). LoadChartOfAccountsFile разворачивает её в плоский список с
	// Parent = код родителя, поэтому у загруженного счёта поле всегда пусто.
	// Раньше ключа в структуре не было, и yaml.v3 молча отбрасывал субсчета.
	Children []Account `yaml:"children,omitempty"`
}

// Виды счёта. Остаток активного счёта — дебетовый, пассивного — кредитовый,
// активно-пассивного — развёрнутый (storage.AccountBalances).
const (
	AccountKindActive        = "active"
	AccountKindPassive       = "passive"
	AccountKindActivePassive = "active_passive"
)

// ValidAccountKind сообщает, что вид счёта — один из известных платформе.
// Непустое неизвестное значение (опечатка, «active-passive» через дефис)
// платформа читала бы как активно-пассивный вид — молча.
func ValidAccountKind(kind string) bool {
	switch kind {
	case AccountKindActive, AccountKindPassive, AccountKindActivePassive:
		return true
	}
	return false
}

// DisplayName возвращает имя счёта с учётом языка.
func (a Account) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := a.Names[lang]; ok && v != "" {
			return v
		}
	}
	return a.Name
}

// ChartOfAccounts is a named set of accounts loaded from YAML.
type ChartOfAccounts struct {
	Name     string            `yaml:"name"`
	Title    string            `yaml:"title"`
	Titles   map[string]string `yaml:"titles"`
	Accounts []Account         `yaml:"accounts"`
}

// DisplayName возвращает заголовок плана счетов с учётом языка.
func (c *ChartOfAccounts) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := c.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if c.Title != "" {
		return c.Title
	}
	return c.Name
}

func LoadChartOfAccountsFile(path string) (*ChartOfAccounts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("accounts: read %s: %w", path, err)
	}
	var chart ChartOfAccounts
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return nil, fmt.Errorf("accounts: parse %s: %w", path, err)
	}
	if chart.Title == "" {
		chart.Title = chart.Name
	}
	accounts, err := flattenAccounts(chart.Accounts, "")
	if err != nil {
		return nil, fmt.Errorf("accounts: %s: %w", filepath.Base(path), err)
	}
	chart.Accounts = accounts
	for i := range chart.Accounts {
		if chart.Accounts[i].Kind == "" {
			chart.Accounts[i].Kind = AccountKindActivePassive
		}
	}
	return &chart, nil
}

// flattenAccounts разворачивает вложенные `children:` в плоский список: субсчёт
// идёт сразу за своим родителем и получает Parent = код родителя. Явный parent у
// вложенного счёта допустим, только если совпадает с тем, куда счёт вложен:
// противоречие — ошибка, а не молчаливый выбор одного из двух родителей.
func flattenAccounts(list []Account, parent string) ([]Account, error) {
	var out []Account
	for _, a := range list {
		children := a.Children
		a.Children = nil
		if parent != "" {
			switch a.Parent {
			case "":
				a.Parent = parent
			case parent:
			default:
				return nil, fmt.Errorf("счёт %q вложен в %q, но указывает parent: %q", a.Code, parent, a.Parent)
			}
		}
		out = append(out, a)
		sub, err := flattenAccounts(children, a.Code)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}

func LoadChartOfAccountsDir(dir string) ([]*ChartOfAccounts, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("accounts: readdir %s: %w", dir, err)
	}
	var charts []*ChartOfAccounts
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		chart, err := LoadChartOfAccountsFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		charts = append(charts, chart)
	}
	return charts, nil
}

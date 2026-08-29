package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AccountRegister is a double-entry bookkeeping register linked to a ChartOfAccounts.
type AccountRegister struct {
	Name      string            `yaml:"name"`
	Title     string            `yaml:"title"`
	Titles    map[string]string `yaml:"titles"`
	Accounts  string            `yaml:"accounts"` // name of the ChartOfAccounts
	Resources []Field           `yaml:"-"`        // parsed from raw
	// Subconto — аналитические разрезы на проводках (по аналогии с 1С). Порядок
	// объявления задаёт нумерацию Субконто1, Субконто2, … для краткой записи в DSL
	// и стабильные имена колонок субконто<N> в таблице регистра.
	Subconto []Field `yaml:"-"`
	// Totals — предрасчёт итогов бухрегистра (план 80): таблица итоги_акк_<name>
	// с помесячными оборотами Дт/Кт по (счёт, субконто…), поддерживаемая в той же
	// транзакции, что и проводки. Enabled включает её и быстрый путь остатков.
	Totals RegisterTotals `yaml:"-"`
}

// DisplayName возвращает заголовок регистра бухгалтерии с учётом языка.
func (ar *AccountRegister) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := ar.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if ar.Title != "" {
		return ar.Title
	}
	return ar.Name
}

// rawAccountRegField — ресурс или субконто бухрегистра в YAML. Отдельный набор
// ключей, а не rawField: у полей бухрегистра нет ни id, ни required, ни default.
type rawAccountRegField struct {
	Name   string            `yaml:"name"`
	Title  string            `yaml:"title"`
	Titles map[string]string `yaml:"titles"`
	Type   string            `yaml:"type"`
	// PII читается не ради исполнения, а ради отказа: см.
	// piiUnsupportedInAccountRegister. Без этого ключа признак не попал бы в
	// разбор вовсе и был бы принят молча — ровно та тишина, против которой
	// заведён сам признак (Field.PII).
	PII bool `yaml:"pii"`
}

type rawAccountReg struct {
	Name      string               `yaml:"name"`
	Title     string               `yaml:"title"`
	Titles    map[string]string    `yaml:"titles"`
	Accounts  string               `yaml:"accounts"`
	Resources []rawAccountRegField `yaml:"resources"`
	Subconto  []rawAccountRegField `yaml:"subconto"`
	Totals    rawTotals            `yaml:"totals"`
}

// piiUnsupportedInAccountRegister — отказ в признаке ПДн у поля бухрегистра.
//
// Умолчание fail-closed само по себе до бухрегистра дотянулось бы: предикатную
// сущность (storage.AccountRegisterPredicateEntity) видит и FieldDecisions, и
// путь запросов. Но списки самого регистра — проводки и остатки
// (/ui/accountreg/*) — не маскируют полей вовсе, ни по умолчанию, ни по явному
// правилу роли. Признак, закрывающий значение в отчёте и показывающий его же
// на соседней странице, — не защита, а её видимость; принимать его молча
// нельзя по той же причине, по которой отвергается pii у поля табличной части.
//
// Снять запрет можно, когда списки бухрегистра пройдут через маскирующую
// границу, как это сделано для регистров накопления (#859) и сведений (#767).
func piiUnsupportedInAccountRegister(reg, role, field string) error {
	return fmt.Errorf("регистр бухгалтерии %s, %s %s: признак pii не поддерживается — "+
		"списки проводок и остатков бухрегистра поля не маскируют, обещанной защиты не будет; снимите pii",
		reg, role, field)
}

func LoadAccountRegisterFile(path string) (*AccountRegister, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("accountreg: read %s: %w", path, err)
	}
	var raw rawAccountReg
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("accountreg: parse %s: %w", path, err)
	}
	ar := &AccountRegister{
		Name:     raw.Name,
		Title:    raw.Title,
		Titles:   raw.Titles,
		Accounts: raw.Accounts,
		Totals:   RegisterTotals{Enabled: raw.Totals.Enabled},
	}
	if ar.Title == "" {
		ar.Title = ar.Name
	}
	for _, r := range raw.Resources {
		if r.PII {
			return nil, piiUnsupportedInAccountRegister(raw.Name, "ресурс", r.Name)
		}
		ar.Resources = append(ar.Resources, parseField(rawField{
			Name:   r.Name,
			Title:  r.Title,
			Titles: r.Titles,
			Type:   r.Type,
		}))
	}
	for _, s := range raw.Subconto {
		if s.PII {
			return nil, piiUnsupportedInAccountRegister(raw.Name, "субконто", s.Name)
		}
		ar.Subconto = append(ar.Subconto, parseField(rawField{
			Name:   s.Name,
			Title:  s.Title,
			Titles: s.Titles,
			Type:   s.Type,
		}))
	}
	return ar, nil
}

func LoadAccountRegisterDir(dir string) ([]*AccountRegister, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("accountreg: readdir %s: %w", dir, err)
	}
	var regs []*AccountRegister
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		ar, err := LoadAccountRegisterFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		regs = append(regs, ar)
	}
	return regs, nil
}

// AccountRegTableName returns the PostgreSQL table name for an account register.
func AccountRegTableName(name string) string {
	return "акк_" + strings.ToLower(name)
}

// SubcontoColumn возвращает имя колонки субконто по его порядковому номеру (1-based).
// Имя стабильно при переименовании поля субконто, что упрощает краткую запись
// СубконтоN в DSL и запросах.
func SubcontoColumn(idx int) string {
	return fmt.Sprintf("субконто%d", idx)
}

// AccountRegTotalsTableName — таблица предрасчитанных итогов бухрегистра (план 80):
// помесячные обороты Дт/Кт по (счёт, субконто…). Отдельная от итоги_<рег>
// накопления, чтобы имена бухрегистра и регистра накопления не сталкивались.
func AccountRegTotalsTableName(name string) string {
	return "итоги_акк_" + strings.ToLower(name)
}

// TotalsEnabled сообщает, что пользователь включил итоги бухрегистра (план 80).
func (ar *AccountRegister) TotalsEnabled() bool { return ar.Totals.Enabled }

// TotalsUsable — применимы ли итоги к бухрегистру. Ограничений уровня атрибутов,
// как у регистра накопления, здесь нет, поэтому — просто включённость.
func (ar *AccountRegister) TotalsUsable() bool { return ar.Totals.Enabled }

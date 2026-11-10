package gherkin

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ШагОпределение — одна запись реестра шагов проекта: текст шага Gherkin и
// DSL, который за ним стоит.
//
// тип: действие — код выполняется; проверка — код вычисляет значение и
// сравнение с ожиданием даёт проверку Утверждать.*.
type StepDef struct {
	Step    string `yaml:"шаг"`      // образец с <параметром>
	Kind    string `yaml:"тип"`      // действие | проверка
	Code    string `yaml:"код"`      // DSL-блок для действия
	Compute string `yaml:"вычисление"` // DSL-блок перед проверкой
	Expr    string `yaml:"выражение"`  // выражение проверки
	Assert  string `yaml:"проверка"`   // Равно | НеРавно | Истина | Ложь | Заполнено | Провалить
	Expect  string `yaml:"ожидание"`   // шаблон ожидаемого значения
	Label   string `yaml:"имя"`      // подпись проверки в отчёте (по умолчанию текст шага)
}

// Реестр шагов проекта: образец → DSL.
type Registry struct {
	Defs  []StepDef
	pats  []*regexp.Regexp
	names [][]string
}

// LoadRegistry читает реестр шагов из YAML. Файл живёт в проекте (features/…),
// компилятор его не создаёт и не правит.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("реестр шагов %s: %w", path, err)
	}
	var doc struct {
		Steps []StepDef `yaml:"шаги"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("реестр шагов %s: %w", path, err)
	}
	if len(doc.Steps) == 0 {
		return nil, fmt.Errorf("реестр шагов %s: пуст (нужен список «шаги:») — тест без шагов не компилируется", path)
	}
	r := &Registry{}
	for i, d := range doc.Steps {
		if strings.TrimSpace(d.Step) == "" {
			return nil, fmt.Errorf("реестр шагов %s: запись %d без «шаг»", path, i+1)
		}
		switch strings.ToLower(strings.TrimSpace(d.Kind)) {
		case "действие":
			if strings.TrimSpace(d.Code) == "" {
				return nil, fmt.Errorf("реестр шагов %s: шаг «%s» (действие) без кода", path, d.Step)
			}
		case "проверка":
			if strings.TrimSpace(d.Expr) == "" {
				return nil, fmt.Errorf("реестр шагов %s: шаг «%s» (проверка) без «выражение»", path, d.Step)
			}
			a := strings.TrimSpace(d.Assert)
			switch a {
			case "Равно", "НеРавно":
				if strings.TrimSpace(d.Expect) == "" {
					return nil, fmt.Errorf("реестр шагов %s: шаг «%s» (проверка %s) без «ожидание»", path, d.Step, a)
				}
			case "Истина", "Ложь", "Заполнено", "Провалить":
				if strings.TrimSpace(d.Expect) != "" {
					return nil, fmt.Errorf("реестр шагов %s: шаг «%s» (проверка %s) не принимает «ожидание»", path, d.Step, a)
				}
			default:
				return nil, fmt.Errorf("реестр шагов %s: шаг «%s»: неизвестная проверка %q (доступны Равно, НеРавно, Истина, Ложь, Заполнено, Провалить)", path, d.Step, d.Assert)
			}
		default:
			return nil, fmt.Errorf("реестр шагов %s: шаг «%s»: тип %q (доступны действие, проверка)", path, d.Step, d.Kind)
		}
		re, names, err := compilePattern(d.Step)
		if err != nil {
			return nil, fmt.Errorf("реестр шагов %s: шаг «%s»: %w", path, d.Step, err)
		}
		r.Defs = append(r.Defs, d)
		r.pats = append(r.pats, re)
		r.names = append(r.names, names)
	}
	return r, nil
}

// compilePattern переводит образец «заведён проект «<Проект>»» в регистр
// регистронезависимо; <параметр> становится группой, значения которой не
// содержат закрывающей кавыки, чтобы «a» и «b» не слиплись в один захват.
func compilePattern(pattern string) (*regexp.Regexp, []string, error) {
	var sb strings.Builder
	sb.WriteString(`(?i)^\s*`)
	var names []string
	rest := pattern
	for {
		i := strings.Index(rest, "<")
		if i < 0 {
			sb.WriteString(regexp.QuoteMeta(rest))
			break
		}
		j := strings.Index(rest[i:], ">")
		if j < 0 {
			return nil, nil, fmt.Errorf("незакрытый <параметр> в образце %q", pattern)
		}
		name := rest[i+1 : i+j]
		if name == "" {
			return nil, nil, fmt.Errorf("пустой <параметр> в образце %q", pattern)
		}
		sb.WriteString(regexp.QuoteMeta(rest[:i]))
		sb.WriteString(`([^"»]+?)`)
		names = append(names, name)
		rest = rest[i+j+1:]
	}
	sb.WriteString(`\s*$`)
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return nil, nil, fmt.Errorf("образец %q: %w", pattern, err)
	}
	return re, names, nil
}

// Match находит реестровый шаг для текста шага и возвращает значения его
// параметров. nil — шага нет в реестре (шаг не компилируется).
func (r *Registry) Match(text string) (*StepDef, map[string]string) {
	trimmed := strings.TrimSpace(text)
	for i, re := range r.pats {
		m := re.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		values := make(map[string]string, len(r.names[i]))
		for k, name := range r.names[i] {
			values[name] = strings.TrimSpace(m[k+1])
		}
		return &r.Defs[i], values
	}
	return nil, nil
}

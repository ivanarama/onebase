package ui

import (
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/ivantit66/onebase/internal/metadata"
)

type qbField struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"` // string|number|date|bool|ref|dim|res|attr
}

type qbSource struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Group   string    `json:"group"`
	VTParam string    `json:"vtParam,omitempty"` // e.g. "&НаДату" or "&Начало,&Конец"
	Fields  []qbField `json:"fields"`
}

func (s *Server) queryBuilder(w http.ResponseWriter, r *http.Request) {
	sources := s.buildQuerySources()
	schemaJSON, _ := json.Marshal(sources)
	s.render(w, r, "page-query-builder", map[string]any{
		"Schema": template.JS(schemaJSON), //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
	})
}

func (s *Server) buildQuerySources() []qbSource {
	var sources []qbSource

	// Catalogs
	for _, e := range s.reg.Entities() {
		if e.Kind != metadata.KindCatalog {
			continue
		}
		src := qbSource{
			ID:    "catalog:" + e.Name,
			Label: "Справочник." + e.Name,
			Group: "Справочники",
		}
		for _, f := range e.Fields {
			src.Fields = append(src.Fields, qbField{Name: f.Name, Label: f.Name, Type: fieldTypeName(f.Type)})
		}
		sources = append(sources, src)
	}

	// Documents
	for _, e := range s.reg.Entities() {
		if e.Kind != metadata.KindDocument {
			continue
		}
		src := qbSource{
			ID:    "document:" + e.Name,
			Label: "Документ." + e.Name,
			Group: "Документы",
		}
		for _, f := range e.Fields {
			src.Fields = append(src.Fields, qbField{Name: f.Name, Label: f.Name, Type: fieldTypeName(f.Type)})
		}
		sources = append(sources, src)
	}

	// Accumulation registers
	for _, reg := range s.reg.Registers() {
		raw := qbSource{
			ID:    "register:" + reg.Name,
			Label: "РегистрНакопления." + reg.Name,
			Group: "Регистры накопления",
		}
		raw.Fields = append(raw.Fields, qbField{Name: "период", Label: "Период", Type: "date"})
		raw.Fields = append(raw.Fields, qbField{Name: "вид_движения", Label: "ВидДвижения", Type: "string"})
		for _, f := range reg.Dimensions {
			raw.Fields = append(raw.Fields, qbField{Name: f.Name, Label: f.Name, Type: "dim"})
		}
		for _, f := range reg.Resources {
			raw.Fields = append(raw.Fields, qbField{Name: f.Name, Label: f.Name, Type: "res"})
		}
		sources = append(sources, raw)

		bal := qbSource{
			ID:      "vt_balances:" + reg.Name,
			Label:   "РегистрНакопления." + reg.Name + ".Остатки(&НаДату)",
			Group:   "Виртуальные таблицы",
			VTParam: "&НаДату",
		}
		for _, f := range reg.Dimensions {
			bal.Fields = append(bal.Fields, qbField{Name: f.Name, Label: f.Name, Type: "dim"})
		}
		for _, f := range reg.Resources {
			bal.Fields = append(bal.Fields, qbField{Name: f.Name + "Остаток", Label: f.Name + "Остаток", Type: "res"})
		}
		sources = append(sources, bal)

		trn := qbSource{
			ID:      "vt_turnovers:" + reg.Name,
			Label:   "РегистрНакопления." + reg.Name + ".Обороты(&Начало, &Конец)",
			Group:   "Виртуальные таблицы",
			VTParam: "&Начало, &Конец",
		}
		for _, f := range reg.Dimensions {
			trn.Fields = append(trn.Fields, qbField{Name: f.Name, Label: f.Name, Type: "dim"})
		}
		for _, f := range reg.Resources {
			trn.Fields = append(trn.Fields, qbField{Name: f.Name + "Приход", Label: f.Name + "Приход", Type: "res"})
			trn.Fields = append(trn.Fields, qbField{Name: f.Name + "Расход", Label: f.Name + "Расход", Type: "res"})
			trn.Fields = append(trn.Fields, qbField{Name: f.Name + "Оборот", Label: f.Name + "Оборот", Type: "res"})
		}
		sources = append(sources, trn)
	}

	// Info registers
	for _, ir := range s.reg.InfoRegisters() {
		raw := qbSource{
			ID:    "inforeg:" + ir.Name,
			Label: "РегистрСведений." + ir.Name,
			Group: "Регистры сведений",
		}
		if ir.Periodic {
			raw.Fields = append(raw.Fields, qbField{Name: "period", Label: "Период", Type: "date"})
		}
		for _, f := range ir.Dimensions {
			raw.Fields = append(raw.Fields, qbField{Name: f.Name, Label: f.Name, Type: "dim"})
		}
		for _, f := range ir.Resources {
			raw.Fields = append(raw.Fields, qbField{Name: f.Name, Label: f.Name, Type: "res"})
		}
		sources = append(sources, raw)

		sl := qbSource{
			ID:      "vt_slice:" + ir.Name,
			Label:   "РегистрСведений." + ir.Name + ".СрезПоследних(&НаДату)",
			Group:   "Виртуальные таблицы",
			VTParam: "&НаДату",
		}
		for _, f := range ir.Dimensions {
			sl.Fields = append(sl.Fields, qbField{Name: f.Name, Label: f.Name, Type: "dim"})
		}
		for _, f := range ir.Resources {
			sl.Fields = append(sl.Fields, qbField{Name: f.Name, Label: f.Name, Type: "res"})
		}
		sources = append(sources, sl)
	}

	// Account registers
	for _, ar := range s.reg.AccountRegisters() {
		bal := qbSource{
			ID:      "vt_acct_bal:" + ar.Name,
			Label:   "РегистрБухгалтерии." + ar.Name + ".Остатки(&НаДату)",
			Group:   "Регистры бухгалтерии",
			VTParam: "&НаДату",
		}
		bal.Fields = append(bal.Fields, qbField{Name: "Счёт", Label: "Счёт", Type: "string"})
		bal.Fields = append(bal.Fields, qbField{Name: "Наименование", Label: "Наименование", Type: "string"})
		for _, f := range ar.Resources {
			bal.Fields = append(bal.Fields, qbField{Name: f.Name + "Остаток", Label: f.Name + "Остаток", Type: "res"})
			bal.Fields = append(bal.Fields, qbField{Name: f.Name + "Дт", Label: f.Name + "Дт", Type: "res"})
			bal.Fields = append(bal.Fields, qbField{Name: f.Name + "Кт", Label: f.Name + "Кт", Type: "res"})
		}
		sources = append(sources, bal)

		trn := qbSource{
			ID:      "vt_acct_trn:" + ar.Name,
			Label:   "РегистрБухгалтерии." + ar.Name + ".Обороты(&Начало, &Конец)",
			Group:   "Регистры бухгалтерии",
			VTParam: "&Начало, &Конец",
		}
		trn.Fields = append(trn.Fields, qbField{Name: "Счёт", Label: "Счёт", Type: "string"})
		trn.Fields = append(trn.Fields, qbField{Name: "Наименование", Label: "Наименование", Type: "string"})
		for _, f := range ar.Resources {
			trn.Fields = append(trn.Fields, qbField{Name: f.Name + "Дт", Label: f.Name + "Дт", Type: "res"})
			trn.Fields = append(trn.Fields, qbField{Name: f.Name + "Кт", Label: f.Name + "Кт", Type: "res"})
		}
		sources = append(sources, trn)
	}

	return sources
}

func fieldTypeName(t metadata.FieldType) string {
	switch {
	case t == metadata.FieldTypeNumber:
		return "number"
	case t == metadata.FieldTypeDate:
		return "date"
	case t == metadata.FieldTypeBool:
		return "bool"
	case metadata.IsReference(t):
		return "ref"
	case metadata.IsEnum(t):
		return "string"
	default:
		return "string"
	}
}

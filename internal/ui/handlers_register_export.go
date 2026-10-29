package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

func registerExcelURL(r *http.Request, path string) string {
	if query := r.URL.Query().Encode(); query != "" {
		return path + "?" + query
	}
	return path
}

func (s *Server) registerMovementsExcel(w http.ResponseWriter, r *http.Request) {
	data, ok := s.registerMovementsData(w, r)
	if !ok {
		return
	}
	reg := data["Register"].(*metadata.Register)
	var fields []metadata.Field
	lang := s.resolveLang(r)
	if data["ShowMovementKind"].(bool) {
		fields = append(fields, metadata.Field{Name: "вид_движения", Title: s.tr(lang, "Вид движения")})
	}
	if data["ShowRecorder"].(bool) {
		fields = append(fields, metadata.Field{Name: "recorder_label", Title: s.tr(lang, "Регистратор")})
		for _, row := range data["Rows"].([]map[string]any) {
			if asString(row["recorder_label"]) == "" {
				// Match the HTML fallback; a raw recorder UUID is never a label.
				row["recorder_label"] = row["recorder_type"]
			}
		}
	}
	for _, key := range []string{"VisibleDimensions", "VisibleResources", "VisibleAttributes"} {
		fields = append(fields, data[key].([]metadata.Field)...)
	}
	s.writeRegisterExcel(w, r, reg.Name, fields, data["Rows"].([]map[string]any), false)
}

func (s *Server) registerBalancesExcel(w http.ResponseWriter, r *http.Request) {
	data, ok := s.registerBalancesData(w, r)
	if !ok {
		return
	}
	reg := data["Register"].(*metadata.Register)
	fields := append([]metadata.Field{}, data["VisibleDimensions"].([]metadata.Field)...)
	fields = append(fields, data["VisibleResources"].([]metadata.Field)...)
	s.writeRegisterExcel(w, r, reg.Name, fields, data["Rows"].([]map[string]any), false)
}

func (s *Server) infoRegExcel(w http.ResponseWriter, r *http.Request) {
	data, ok := s.infoRegListData(w, r)
	if !ok {
		return
	}
	ir := data["InfoReg"].(*metadata.InfoRegister)
	var fields []metadata.Field
	if ir.Periodic {
		fields = append(fields, metadata.Field{Name: "period", Title: s.tr(s.resolveLang(r), "Период"), Type: metadata.FieldTypeDate})
	}
	fields = append(fields, ir.Dimensions...)
	fields = append(fields, ir.Resources...)
	s.writeRegisterExcel(w, r, ir.Name, fields, data["Rows"].([]map[string]any), true)
}

// registerExcelValue keeps masked/presentation values as text. Excel's numeric
// precision is limited: values that cannot round-trip through a 15-digit number
// remain text rather than silently changing a business value.
func registerExcelValue(field metadata.Field, value any) any {
	if value == nil {
		return ""
	}
	if field.RefEntity != "" {
		return fmt.Sprint(value)
	}
	switch field.Type {
	case metadata.FieldTypeNumber:
		text := fmt.Sprint(value)
		if number, err := decimal.NewFromString(text); err == nil && number.Exponent() >= -308 && number.Exponent() <= 308 {
			if f, err := strconv.ParseFloat(text, 64); err == nil &&
				len(number.Abs().Coefficient().String()) <= 15 && decimal.NewFromFloat(f).Equal(number) {
				return f
			}
		}
		return text
	case metadata.FieldTypeDate:
		if date, ok := value.(time.Time); ok {
			return date
		}
		if date, ok := storage.ParseRegPeriod(fmt.Sprint(value)); ok {
			return date
		}
		// The shared masking boundary also formats readable register dates
		// for HTML. Recover their local wall time without parsing masked text.
		for _, layout := range []string{"02.01.2006 15:04:05", "02.01.2006"} {
			if date, err := time.ParseInLocation(layout, fmt.Sprint(value), time.Local); err == nil {
				return date
			}
		}
	}
	return value
}

func (s *Server) writeRegisterExcel(w http.ResponseWriter, r *http.Request, name string, fields []metadata.Field, rows []map[string]any, labelSuffix bool) {
	book := excelize.NewFile()
	defer func() { _ = book.Close() }()
	dateStyle, err := book.NewStyle(&excelize.Style{CustomNumFmt: new("yyyy-mm-dd hh:mm:ss")})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	write := func() error {
		for col, field := range fields {
			cell, _ := excelize.CoordinatesToCellName(col+1, 1)
			if err := book.SetCellValue("Sheet1", cell, field.DisplayName(s.resolveLang(r))); err != nil {
				return err
			}
			for rowIndex, row := range rows {
				value := row[field.Name]
				if labelSuffix && asString(row[field.Name+"_label"]) != "" {
					value = row[field.Name+"_label"]
				}
				value = registerExcelValue(field, value)
				cell, _ = excelize.CoordinatesToCellName(col+1, rowIndex+2)
				if err := book.SetCellValue("Sheet1", cell, value); err != nil {
					return err
				}
				if _, date := value.(time.Time); date {
					if err := book.SetCellStyle("Sheet1", cell, cell, dateStyle); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := write(); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", contentDisposition(name+".xlsx"))
	if err := book.Write(w); err != nil {
		// The response may already have started; retain the existing export
		// error contract without attempting a second workbook.
		http.Error(w, "Excel error: "+s.errText(r, err), http.StatusInternalServerError)
	}
}

package metadata

import "fmt"

// InfoRegisterPeriodField — служебное поле периода, допустимое в индексе
// периодического регистра сведений (так же оно называется в запросах).
const InfoRegisterPeriodField = "period"

// ValidateInfoRegisterIndexes проверяет indexes регистра сведений: поля —
// измерения, ресурсы или period (только у периодического). Уникальный индекс
// запрещён: уникальность записи регистра задаёт ключ измерений, а второй
// уникальный ключ отклонял бы обычную запись набора без внятной причины.
func ValidateInfoRegisterIndexes(ir *InfoRegister) error {
	for i, idx := range ir.Indexes {
		if len(idx.Fields) == 0 {
			return fmt.Errorf("info register %s: indexes[%d].fields is required", ir.Name, i)
		}
		if idx.Unique {
			return fmt.Errorf("info register %s: indexes[%d]: unique не поддерживается — уникальность записи задают измерения", ir.Name, i)
		}
		for _, name := range idx.Fields {
			if _, ok := InfoRegisterIndexColumn(ir, name); !ok {
				return fmt.Errorf("info register %s: index references unknown field %s", ir.Name, name)
			}
		}
	}
	return nil
}

// InfoRegisterIndexColumn — колонка таблицы регистра для поля индекса.
func InfoRegisterIndexColumn(ir *InfoRegister, name string) (string, bool) {
	if name == InfoRegisterPeriodField {
		return "period", ir.Periodic
	}
	for _, group := range [][]Field{ir.Dimensions, ir.Resources} {
		for _, f := range group {
			if f.Name == name {
				return ColumnName(f), true
			}
		}
	}
	return "", false
}

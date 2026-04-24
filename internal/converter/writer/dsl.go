package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
)

// WriteDSLStubs создаёт заготовки .os файлов для документов.
// Если рядом есть .bsl-модуль из 1С — добавляет его содержимое как комментарий.
func WriteDSLStubs(docs []*parser1c.DocumentMeta, srcDir1C, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "src")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	for _, doc := range docs {
		stub := buildStub(doc, srcDir1C)
		name := strings.ToLower(doc.Name) + ".os"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(stub), 0o644); err != nil {
			return err
		}
		notes.DSLStubs = append(notes.DSLStubs, name)
	}
	return nil
}

func buildStub(doc *parser1c.DocumentMeta, srcDir1C string) string {
	var sb strings.Builder

	sb.WriteString("Процедура ПриЗаписи()\n")
	sb.WriteString("  // TODO: перенесите бизнес-логику из модуля 1С\n")
	sb.WriteString("  //\n")
	sb.WriteString("  // Доступные реквизиты документа:\n")
	for _, f := range doc.Attributes {
		sb.WriteString(fmt.Sprintf("  //   this.%s\n", f.Name))
	}
	for _, ts := range doc.TabularSections {
		sb.WriteString(fmt.Sprintf("  //\n  // Табличная часть %s:\n", ts.Name))
		sb.WriteString(fmt.Sprintf("  //   Для Каждого Строка Из this.%s Цикл\n", ts.Name))
		for _, f := range ts.Attributes {
			sb.WriteString(fmt.Sprintf("  //     Строка.%s\n", f.Name))
		}
		sb.WriteString("  //   КонецЦикла;\n")
	}
	sb.WriteString("\n")
	sb.WriteString("  // Пример валидации:\n")
	sb.WriteString("  // Если this.Номер = \"\" Тогда\n")
	sb.WriteString("  //   Error(\"Номер обязателен\");\n")
	sb.WriteString("  // КонецЕсли;\n")

	// Добавить исходный .bsl если нашли
	bslPath := filepath.Join(srcDir1C, "Documents", doc.Name, "Ext", "ObjectModule.bsl")
	if bsl, err := os.ReadFile(bslPath); err == nil {
		sb.WriteString("\n  // ======= Исходный модуль 1С (.bsl) =======\n")
		for _, line := range strings.Split(string(bsl), "\n") {
			sb.WriteString("  // ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("  // ==========================================\n")
	}

	sb.WriteString("КонецПроцедуры\n")
	return sb.String()
}

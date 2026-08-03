package writer

import (
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
)

// preprocDirectiveLow матчит строку-директиву препроцессора 1С (рус/англ),
// применяется к strings.ToLower(line) — обходит ограничение RE2 на (?i) с кириллицей.
// \b не используется (RE2 работает только с ASCII-границами); вместо этого
// после ключевого слова требуется пробел, конец строки или скобка.
var preprocDirectiveLow = regexp.MustCompile(`^\s*#\s*(область|конецобласти|иначеесли|иначе|конецесли|если|endregion|region|elsif|endif|else|if)(\s|$)`)

// sanitizeBSL убирает из исходника 1С строки-директивы препроцессора:
// DSL OneBase их не поддерживает (issue #48 п.2). Содержимое блоков #Если
// сохраняется целиком (обе ветки).
func sanitizeBSL(src string) string {
	// .bsl из 1С обычно начинается с BOM — без среза первая директива не распознаётся.
	src = strings.TrimPrefix(src, "\xef\xbb\xbf")
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if preprocDirectiveLow.MatchString(strings.ToLower(line)) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// WriteDSLStubs создаёт заготовки .os файлов для документов.
// Если рядом есть .bsl-модуль из 1С — добавляет его содержимое как комментарий.
func WriteDSLStubs(docs []*parser1c.DocumentMeta, srcDir1C, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "src")
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}

	for _, doc := range docs {
		stub := buildStub(doc, srcDir1C)
		name := strings.ToLower(doc.Name) + ".os"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(stub), fsmode.File); err != nil {
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
		fmt.Fprintf(&sb, "  //   this.%s\n", f.Name)
	}
	for _, ts := range doc.TabularSections {
		fmt.Fprintf(&sb, "  //\n  // Табличная часть %s:\n", ts.Name)
		fmt.Fprintf(&sb, "  //   Для Каждого Строка Из this.%s Цикл\n", ts.Name)
		for _, f := range ts.Attributes {
			fmt.Fprintf(&sb, "  //     Строка.%s\n", f.Name)
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
		for _, line := range strings.Split(sanitizeBSL(string(bsl)), "\n") {
			sb.WriteString("  // ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("  // ==========================================\n")
	}

	sb.WriteString("КонецПроцедуры\n")
	return sb.String()
}

// WriteModules записывает общие модули в out/src/*.module.os.
func WriteModules(mods []*parser1c.ModuleMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "src")
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}
	for _, mod := range mods {
		source := sanitizeBSL(mod.Source)
		if source == "" {
			source = fmt.Sprintf("// %s\n// Общий модуль\n\nПроцедура Главная()\nКонецПроцедуры\n", mod.Name)
		}
		name := strings.ToLower(mod.Name) + ".module.os"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(source), fsmode.File); err != nil {
			return err
		}
		notes.Modules++
	}
	return nil
}

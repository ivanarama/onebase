package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Извлекается тело inline-скрипта, а не всё подряд. Каждый случай здесь —
// ложное срабатывание, которое реально было при наивном подходе: без него гейт
// пришлось бы отключить в первый же прогон.
func TestExtract(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "простой скрипт",
			in:   `<script>var a = 1;</script>`,
			want: []string{"var a = 1;"},
		},
		{
			name: "внешний src пропускается — тела нет",
			in:   `<script src="/static/ui.js"></script>`,
			want: nil,
		},
		{
			name: "data-src не является src и не скрывает исполняемый код",
			in:   `<script data-src="metadata">function broken( {</script>`,
			want: []string{"function broken( {"},
		},
		{
			name: "имена атрибутов внутри значения не принимаются за атрибуты",
			in:   `<script data-note=" src=x type=application/json">let executed = true;</script>`,
			want: []string{"let executed = true;"},
		},
		{
			name: "application/json — данные, а не код",
			in:   `<script type="application/json" id="x">{"a":1}</script>`,
			want: nil,
		},
		{
			name: "data-type не является type",
			in:   `<script data-type="application/json">let executed = true;</script>`,
			want: []string{"let executed = true;"},
		},
		{
			name: "application/javascript исполняется",
			in:   `<script type="application/javascript">let executed = true;</script>`,
			want: []string{"let executed = true;"},
		},
		{
			name: "действие шаблона заменяется на плейсхолдер",
			in:   `<script>var cfg = {{jsJSON .Cfg}};</script>`,
			want: []string{"var cfg = 0;"},
		},
		{
			name: "каждая ветвь в позиции значения проверяется отдельно",
			in:   `<script>window.__cfg = {{if .Bootstrap}}{{.Bootstrap}}{{else}}{}{{end}};</script>`,
			want: []string{"window.__cfg = 0;", "window.__cfg = {};"},
		},
		{
			name: "процент вне fmt-вызова не исправляется",
			in:   `<script>post({id:%s});</script>`,
			want: []string{"post({id:%s});"},
		},
		{
			name: "тип module допустим",
			in:   `<script type="module">import x from "y";</script>`,
			want: []string{`import x from "y";`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := extract(c.in)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("извлечено %d блоков (%v), ожидалось %d", len(got), got, len(c.want))
			}
			for i := range got {
				if strings.TrimSpace(got[i].body) != c.want[i] {
					t.Errorf("блок %d = %q, ожидалось %q", i, got[i].body, c.want[i])
				}
			}
			if c.name == "тип module допустим" && (len(got) != 1 || !got[0].module) {
				t.Errorf("type=module потерян: %+v", got)
			}
		})
	}
}

// Упоминание <script> внутри комментария — не скрипт.
//
// Это ловушка, на которой наивная версия падала: регексп начинал «тело» внутри
// пояснения и тянул его до ближайшего закрывающего тега, а node сообщал об
// ошибке в тексте комментария. Шесть ложных срабатываний из сорока восьми
// блоков — гейт с такой точностью не пережил бы и одного прогона.
func TestExtractFile_КомментарииНеСкрипты(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmpl.go")
	src := "package p\n\nconst t = `" + `
<!-- Ниже идёт <script>, чтобы window.__cfg существовал. -->
{{/* form-shared-js — общий <script> блок формы. */}}
<script>var ok = 1;</script>
` + "`\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	blocks, torn, err := extractFile(path)
	if err != nil {
		t.Fatalf("extractFile: %v", err)
	}
	if torn != 0 {
		t.Errorf("непарных тегов %d, а их нет: теги в комментариях считаться не должны", torn)
	}
	if len(blocks) != 1 {
		t.Fatalf("извлечено %d блоков, ожидался 1: %+v", len(blocks), blocks)
	}
	if got := strings.TrimSpace(blocks[0].body); got != "var ok = 1;" {
		t.Errorf("тело = %q, ожидалось «var ok = 1;»", got)
	}
	if blocks[0].line == 0 {
		t.Error("нет номера строки — сообщение node будет указывать во временный файл, и искать негде")
	}
}

// Литерал не склеивается со следующим: `<script>` в одном и `</script>` в
// другом — непроверенный блок, который обязан остановить гейт. Именно на этом
// наивный регексп по тексту файла ломался, а суммарный счётчик тегов ошибочно
// взаимно гасил две половины.
func TestExtractFile_ТегиИзРазныхЛитераловFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "split.go")
	src := "package p\n\nvar a = \"<script>\"\nvar broken = 1 +\n\t2\nvar b = \"</script>\"\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	blocks, torn, err := extractFile(path)
	if err != nil {
		t.Fatalf("extractFile: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("склеены теги из разных литералов: %+v", blocks)
	}
	if torn != 2 {
		t.Errorf("непарных тегов %d, ожидалось 2", torn)
	}
}

// Сломанный inline-скрипт доезжает до node дословно: гейт обязан его завалить,
// а не «починить» плейсхолдером.
func TestExtract_СломанныйСкриптНеЧинится(t *testing.T) {
	got, err := extract(`<script>function f( { var x = ;</script>`)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("извлечено %d блоков, ожидался 1", len(got))
	}
	if !strings.Contains(got[0].body, "function f( { var x = ;") {
		t.Errorf("тело изменено при извлечении: %q — node проверит не то, что в шаблоне", got[0].body)
	}
}

func TestExtract_ПроверяетКаждуюВетвьШаблона(t *testing.T) {
	got, err := extract(`<script>
window.__cfg = {{if .Bootstrap}}{{.Bootstrap}}{{else}}{}{{end}};
{{if .Enabled}}function broken( { var x = ;{{end}}
</script>`)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("вариантов = %d, ожидалось 4: %+v", len(got), got)
	}
	brokenPreserved := false
	for _, script := range got {
		if strings.Contains(script.body, "function broken( { var x = ;") {
			brokenPreserved = true
		}
		if strings.Contains(script.body, "{{") {
			t.Errorf("в варианте осталось действие шаблона: %q", script.body)
		}
	}
	if !brokenPreserved {
		t.Fatal("условная синтаксическая ошибка исчезла при извлечении")
	}
}

func TestExtract_НезакрытыйШаблонFailClosed(t *testing.T) {
	if _, err := extract(`<script>{{if .Enabled}}let ok = true;</script>`); err == nil {
		t.Fatal("незакрытый {{if}} принят — гейт должен падать, а не сокращать покрытие")
	}
}

func TestExtract_ModuleAttributeForms(t *testing.T) {
	for _, source := range []string{
		`<SCRIPT TYPE='MODULE'>import x from "y";</SCRIPT>`,
		`<script type=module>import x from "y";</script>`,
	} {
		got, err := extract(source)
		if err != nil {
			t.Fatalf("extract(%q): %v", source, err)
		}
		if len(got) != 1 || !got[0].module {
			t.Errorf("module не распознан в %q: %+v", source, got)
		}
	}
	if got := outputExtension(true); got != ".mjs" {
		t.Errorf("расширение module-скрипта = %q, ожидалось .mjs", got)
	}
	if got := outputExtension(false); got != ".js" {
		t.Errorf("расширение обычного скрипта = %q, ожидалось .js", got)
	}
}

func TestExtractFile_FormatDirectivesOnlyInFmtCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "format.go")
	src := "package p\n\nimport \"fmt\"\n\nvar raw = `<script>const x = %s;</script>`\nvar formatted = fmt.Sprintf(`<script>const x = %s; const r = total %% size;</script>`, 1)\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	blocks, torn, err := extractFile(path)
	if err != nil {
		t.Fatalf("extractFile: %v", err)
	}
	if torn != 0 || len(blocks) != 2 {
		t.Fatalf("blocks=%+v, torn=%d", blocks, torn)
	}
	if got := strings.TrimSpace(blocks[0].body); got != "const x = %s;" {
		t.Errorf("сырой литерал был ошибочно нормализован: %q", got)
	}
	if got := strings.TrimSpace(blocks[1].body); got != "const x = 0; const r = total % size;" {
		t.Errorf("fmt-литерал нормализован неверно: %q", got)
	}
}

func TestExtractFile_КонкатенацияСобираетсяБезПотериСкрипта(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concat.go")
	src := "package p\n\nvar id = \"x\"\nvar page = `<script>fetch('/` + id + `');</script>`\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	blocks, torn, err := extractFile(path)
	if err != nil {
		t.Fatalf("extractFile: %v", err)
	}
	if torn != 0 || len(blocks) != 1 {
		t.Fatalf("blocks=%+v, torn=%d", blocks, torn)
	}
	if got := strings.TrimSpace(blocks[0].body); got != "fetch('//* Go value omitted by jsextract */');" {
		t.Errorf("конкатенация восстановлена неверно: %q", got)
	}
}

func TestExtractFile_ЗначениеКонкатенацииВПозицииВыражения(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "expression.go")
	src := "package p\n\nvar cfg string\nvar page = `<script>window.cfg = ` + cfg + `;</script>`\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	blocks, torn, err := extractFile(path)
	if err != nil {
		t.Fatalf("extractFile: %v", err)
	}
	if torn != 0 || len(blocks) != 1 {
		t.Fatalf("blocks=%+v, torn=%d", blocks, torn)
	}
	if got := strings.TrimSpace(blocks[0].body); got != "window.cfg = 0;" {
		t.Errorf("динамическое выражение замещено неверно: %q", got)
	}
}

func TestExtractFile_HTMLCommentMarkersInsideScriptArePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.go")
	src := "package p\n\nconst page = `<script>const a=\"<!--\"; function broken( { const b=\"-->\";</script>`\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	blocks, torn, err := extractFile(path)
	if err != nil {
		t.Fatalf("extractFile: %v", err)
	}
	if torn != 0 || len(blocks) != 1 {
		t.Fatalf("blocks=%+v, torn=%d", blocks, torn)
	}
	if !strings.Contains(blocks[0].body, "function broken( {") {
		t.Errorf("JS между comment-маркерами исчез: %q", blocks[0].body)
	}
}

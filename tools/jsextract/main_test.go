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
			name: "application/json — данные, а не код",
			in:   `<script type="application/json" id="x">{"a":1}</script>`,
			want: nil,
		},
		{
			name: "действие шаблона заменяется на плейсхолдер",
			in:   `<script>var cfg = {{jsJSON .Cfg}};</script>`,
			want: []string{"var cfg = 0;"},
		},
		{
			name: "ветвление сворачивается целиком: оно стоит в позиции значения",
			in:   `<script>window.__cfg = {{if .Bootstrap}}{{.Bootstrap}}{{else}}{}{{end}};</script>`,
			want: []string{"window.__cfg = 0;"},
		},
		{
			name: "глагол fmt — такая же интерполяция",
			in:   `<script>post({id:%s});</script>`,
			want: []string{"post({id:0});"},
		},
		{
			name: "тип module допустим",
			in:   `<script type="module">import x from "y";</script>`,
			want: []string{`import x from "y";`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extract(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("извлечено %d блоков (%q), ожидалось %d", len(got), got, len(c.want))
			}
			for i := range got {
				if strings.TrimSpace(got[i]) != c.want[i] {
					t.Errorf("блок %d = %q, ожидалось %q", i, got[i], c.want[i])
				}
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
// другом — не один блок с Go-кодом внутри. Именно на этом наивный регексп по
// тексту файла и ломался.
func TestExtractFile_ТегиИзРазныхЛитераловНеСклеиваются(t *testing.T) {
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
	// Открывающий и закрывающий теги в файле уравновешены, значит блок
	// собирается конкатенацией и проверить его нечем — но и жаловаться не на что.
	if torn != 0 {
		t.Errorf("непарных тегов %d, ожидалось 0", torn)
	}
}

// Сломанный inline-скрипт доезжает до node дословно: гейт обязан его завалить,
// а не «починить» плейсхолдером.
func TestExtract_СломанныйСкриптНеЧинится(t *testing.T) {
	got := extract(`<script>function f( { var x = ;</script>`)
	if len(got) != 1 {
		t.Fatalf("извлечено %d блоков, ожидался 1", len(got))
	}
	if !strings.Contains(got[0], "function f( { var x = ;") {
		t.Errorf("тело изменено при извлечении: %q — node проверит не то, что в шаблоне", got[0])
	}
}

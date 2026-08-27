package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestExtractGoKeys_TemplateLanguageFormsAndTranslationExpressions(t *testing.T) {
	source := []byte("package sample\n\nconst tmpl = `" +
		`{{t .Lang "dot language"}} {{t $.Lang "root language"}}` +
		"`\n\n" +
		`func render(r any, data struct{ Lang string }, s *server, dynamic string) {
	_ = tr(resolveLang(r), "call expression")
	_ = tr(data.Lang, "selector expression")
	_ = s.tr(s.resolveLang(r), "selector call expression")
	_ = tr(data.Lang, dynamic)
}
`)

	keys, err := extractGoKeys(source)
	if err != nil {
		t.Fatalf("extractGoKeys: %v", err)
	}
	want := map[string]bool{
		"call expression":          true,
		"dot language":             true,
		"root language":            true,
		"selector call expression": true,
		"selector expression":      true,
	}
	if len(keys) != len(want) {
		t.Fatalf("keys = %q, want %d keys", keys, len(want))
	}
	for _, key := range keys {
		if !want[key] {
			t.Errorf("unexpected key %q in %q", key, keys)
		}
	}
}

// Язык, у которого пока есть только машинный ярус, обязан попадать в отчёт:
// именно так приезжает каждый новый перевод (человеческий ярус появляется
// позже, при ревью носителем). Отчёт, собранный по одному человеческому ярусу,
// молчал бы о нём вовсе — и «языка нет» было бы не отличить от «язык переведён».
func TestReportCoverage_ListsMachineOnlyLanguage(t *testing.T) {
	keys := []string{"Записать", "Удалить"}
	human := map[string]map[string]string{
		"en": {"Записать": "Save", "Удалить": "Delete"},
		"de": {"Записать": "Speichern"},
	}
	machine := map[string]map[string]string{
		"de": {"Удалить": "Löschen"},
		"be": {"Записать": "Запісаць"},
	}

	var out bytes.Buffer
	reportCoverage(&out, keys, human, machine)

	got := out.String()
	for _, want := range []string{
		"3 locales",
		"  be: 2 не переведено человеком (1 закрыто машинным ярусом, 1 останется по-английски)\n",
		"  de: 1 не переведено человеком (1 закрыто машинным ярусом, 0 останется по-английски)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("отчёт не содержит %q:\n%s", want, got)
		}
	}
}

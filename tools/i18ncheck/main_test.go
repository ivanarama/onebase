package main

import "testing"

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

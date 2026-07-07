package ui

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestDevQueryPagesUseDelegatedHandlers(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want []string
	}{
		{
			name: "page-query-builder",
			data: map[string]any{"Schema": template.JS(`[]`)},
			want: []string{
				`data-ob-qb-source`,
				`data-ob-qb-main-alias`,
				`data-ob-qb-vt-param`,
				`data-ob-qb-action="copy-query"`,
			},
		},
		{
			name: "page-query-console",
			data: map[string]any{"Schema": template.JS(`[]`)},
			want: []string{
				`data-ob-qc-action="exec"`,
				`data-ob-qb-action="apply-editor"`,
				`data-ob-qc-open-picker`,
				`data-ob-qc-picker-close`,
			},
		},
		{
			name: "page-code-console",
			data: map[string]any{},
			want: []string{
				`data-ob-cc-action="exec"`,
				`data-ob-cc-action="clear-output"`,
			},
		},
		{
			name: "page-gengen",
			data: map[string]any{
				"Domains": []map[string]any{{"Name": "trade", "Keywords": "sale, stock"}},
			},
			want: []string{
				`data-ob-gg-action="analyze"`,
				`data-ob-gg-action="copy-path"`,
				`data-ob-gg-toggle-override`,
			},
		},
		{
			name: "page-all-functions",
			data: map[string]any{
				"Catalogs": []map[string]any{{"Name": "Контрагенты"}},
			},
			want: []string{
				`data-ob-af-search`,
				`data-ob-af-toggle`,
			},
		},
		{
			name: "page-forbidden",
			data: map[string]any{},
			want: []string{
				`data-ob-history-back`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.data["Cfg"] = Config{}
			tc.data["Lang"] = "ru"

			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, tc.name, tc.data); err != nil {
				t.Fatalf("execute %s: %v", tc.name, err)
			}
			out := buf.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("%s missing %q:\n%s", tc.name, want, out)
				}
			}
			for _, old := range []string{`onclick=`, `onchange=`, `oninput=`, `.onclick`, `.onchange`, `.oninput`} {
				if strings.Contains(out, old) {
					t.Fatalf("%s still contains inline/property handler %q:\n%s", tc.name, old, out)
				}
			}
		})
	}
}

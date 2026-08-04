package ui

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbcheck"
)

func TestAdminTemplatesUseDelegatedHandlers(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	later := now.Add(24 * time.Hour)
	used := now.Add(time.Hour)

	cases := []struct {
		name string
		tpl  *template.Template
		data map[string]any
		want []string
	}{
		{
			name: "admin-users",
			tpl:  adminTmpl,
			data: map[string]any{"Users": []map[string]any{{
				"ID": "u1", "Login": "ivan", "FullName": "Иван", "CreatedAt": now,
			}}},
			want: []string{`<script src="/static/ui.js"></script>`, `data-ob-confirm="Удалить пользователя ivan?"`},
		},
		{
			name: "admin-passwd",
			tpl:  adminTmpl,
			data: map[string]any{"BackURL": "/ui", "SelfService": true},
			want: []string{`data-ob-confirm="Выйти со всех устройств, кроме текущего?"`},
		},
		{
			name: "admin-sessions",
			tpl:  adminTmpl,
			data: map[string]any{
				"Limit": 3,
				"Sessions": []map[string]any{{
					"Login": "ivan", "FullName": "Иван", "KindLabel": "UI", "CreatedAt": now,
					"LastSeenAt": now, "ExpiresAt": later, "IP": "127.0.0.1", "ShortUA": "test", "PublicID": "pub1",
				}},
			},
			want: []string{
				`data-ob-confirm="Завершить эту сессию ivan?"`,
				`data-ob-confirm="Принудительно завершить все сессии ivan?"`,
			},
		},
		{
			name: "admin-api-tokens",
			tpl:  adminTmpl,
			data: map[string]any{
				"CreatedToken": "secret-token",
				"Users":        []map[string]any{{"ID": "u1", "Login": "ivan"}},
				"Tokens": []map[string]any{{
					"ID": "t1", "Name": "Интеграция", "UserLogin": "ivan", "CreatedAt": now,
					"LastUsedAt": &used, "ExpiresAt": &later,
				}},
			},
			want: []string{`data-ob-select-on-click`, `data-ob-confirm="Отозвать API-токен Интеграция?"`},
		},
		{
			// Диагностика базы (план 114): исправления запускаются только для
			// отмеченных проверок, поэтому подтверждение говорит про них, а не
			// про одну конкретную операцию.
			name: "admin-cleanup",
			tpl:  adminTmpl,
			data: map[string]any{
				"Checks": []doctorCheckView{{
					Result: dbcheck.Result{
						Check: "orphan-movements", Title: "Движения без документа-регистратора",
						Severity: dbcheck.SeverityError, Summary: "движений без регистратора: 1",
						Findings: []dbcheck.Finding{{Object: "Остатки", Detail: "регистратор (Док) не существует", Count: 1}},
						FixHint:  "onebase doctor --fix orphan-movements",
					},
					CanFix: true,
				}},
			},
			want: []string{
				`data-ob-confirm="Выполнить исправления для отмеченных проверок?`,
				`name="fix" value="orphan-movements"`,
			},
		},
		{
			name: "admin-extforms",
			tpl:  extFormTmpl,
			data: map[string]any{"Forms": []map[string]any{{
				"ID": "f1", "Document": "Док", "Name": "Форма", "UploadedAt": now,
			}}},
			want: []string{`data-ob-confirm="Удалить форму Форма?"`},
		},
		{
			name: "admin-extreports",
			tpl:  extReportTmpl,
			data: map[string]any{"Reports": []map[string]any{{
				"ID": "r1", "Name": "Отчёт", "UploadedAt": now,
			}}},
			want: []string{`data-ob-confirm="Удалить отчёт Отчёт?"`},
		},
		{
			name: "admin-extprocessors",
			tpl:  extProcTmpl,
			data: map[string]any{"Procs": []map[string]any{{
				"ID": "p1", "Name": "Обработка", "UploadedAt": now,
			}}},
			want: []string{`data-ob-confirm="Удалить обработку Обработка?"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.tpl.ExecuteTemplate(&buf, tc.name, tc.data); err != nil {
				t.Fatalf("execute %s: %v", tc.name, err)
			}
			out := buf.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("%s missing %q:\n%s", tc.name, want, out)
				}
			}
			for _, old := range []string{`onclick=`, `onchange=`, `oninput=`, `onsubmit=`} {
				if strings.Contains(out, old) {
					t.Fatalf("%s still contains inline handler %q:\n%s", tc.name, old, out)
				}
			}
		})
	}
}

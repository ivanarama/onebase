package ui

// Рендер экранов аутентификации (план 84) и разбор правил маппинга ролей.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestAdminAuthTemplate_PolicyAndProviders(t *testing.T) {
	policy := auth.Policy{
		SSOOnly: true, Require2FAAdmins: true, Require2FARoles: []string{"Бухгалтерия"},
		PasswordMinLength: 10, AllowEmptyPasswords: true,
	}
	var buf bytes.Buffer
	err := adminTmpl.ExecuteTemplate(&buf, "admin-auth", map[string]any{
		"Policy":                          policy,
		"Roles":                           []*auth.Role{{Name: "Бухгалтерия"}, {Name: "Кладовщик"}},
		"RoleSelected":                    map[string]bool{"Бухгалтерия": true},
		"RoleSelectSize":                  3,
		"PasswordPolicyTitle":             "Политика паролей",
		"PasswordMinLengthLabel":          "Минимальная длина пароля",
		"PasswordMinLength":               10,
		"EffectivePasswordMinLength":      10,
		"EffectivePasswordMinLengthLabel": "Действующее значение:",
		"PasswordMinLengthSource":         "настройка базы",
		"PasswordMinLengthRangeHint":      "Минимум — от 1 до 72 символов. Сам пароль — не более 72 байт UTF-8 из-за ограничения bcrypt.",
		"PasswordMinLengthInheritHint":    "Пустое поле наследует умолчание процесса.",
		"DefaultPasswordMinLength":        auth.DefaultMinPasswordLength,
		"MaxPasswordLength":               auth.MaxPasswordLength,
		"AllowEmptyPasswordsLabel":        "Разрешить пустые пароли",
		"EmptyPasswordWarning":            "Учётная запись с пустым паролем защищена только логином.",
		"EmptyPasswordEnvHint":            "",
		"EmptyPasswordsByEnv":             false,
		"SavePasswordPolicyLabel":         "Сохранить политику паролей",
		"Providers": []*auth.OIDCProvider{
			{ID: "keycloak", Name: "Корпоративный вход", Issuer: "https://id.example.com", Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`action="/ui/admin/auth/policy"`,
		`name="require_2fa_admins" value="1" checked`,
		`name="sso_only" value="1" checked`,
		`<option value="Бухгалтерия" selected>`,
		`/ui/admin/auth/providers/keycloak`,
		"ONEBASE_ALLOW_PASSWORD_LOGIN",
		`action="/ui/admin/auth/password-policy"`,
		`name="password_min_length" min="1" max="72" value="10"`,
		`name="allow_empty_passwords" value="1" checked`,
		"72 байт UTF-8",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в HTML нет %q", want)
		}
	}
}

func TestAdminAuthProviderTemplate_HidesStoredSecret(t *testing.T) {
	p := &auth.OIDCProvider{
		ID: "keycloak", Name: "Корпоративный вход",
		Issuer: "https://id.example.com", ClientID: "onebase",
		ClientSecret: "enc:0123456789",
		RoleMappings: []auth.OIDCRoleMapping{{Claim: "groups", Value: "erp-buh", Role: "Бухгалтерия"}},
	}
	var buf bytes.Buffer
	err := adminTmpl.ExecuteTemplate(&buf, "admin-auth-provider", map[string]any{
		"P": p, "IsNew": false, "Error": "",
		"Scopes": strings.Join(p.ScopeList(), " "), "DefaultRoles": "",
		"RoleMappings":  formatRoleMappings(p.RoleMappings),
		"SecretDisplay": secretDisplay(p.ClientSecret),
		"HasMasterKey":  true,
		"RedirectURI":   "https://erp.example.com/auth/oidc/keycloak/callback",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	// Зашифрованный секрет наружу не отдаётся даже в зашифрованном виде.
	if strings.Contains(html, "enc:0123456789") {
		t.Error("сохранённый секрет попал в HTML формы")
	}
	for _, want := range []string{
		"groups = erp-buh -&gt; Бухгалтерия",
		"https://erp.example.com/auth/oidc/keycloak/callback",
		`value="openid email profile"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в HTML нет %q", want)
		}
	}
}

func TestProfile2FATemplate_StatesRender(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want []string
	}{
		{"выключен", map[string]any{
			"Info": auth.TwoFactorInfo{}, "Required": true,
		}, []string{"Второй фактор выключен", "Политика базы требует"}},
		{"включён", map[string]any{
			"Info": auth.TwoFactorInfo{Enabled: true, BackupCodesLeft: 7}, "Required": false,
		}, []string{"Второй фактор включён", ">7<", `value="disable"`}},
		{"настройка", map[string]any{
			"Info": auth.TwoFactorInfo{}, "Setup": true, "Secret": "ABCD EFGH",
		}, []string{"/ui/profile/2fa/qr", "ABCD EFGH", `value="confirm"`}},
		{"резервные коды", map[string]any{
			"Info": auth.TwoFactorInfo{Enabled: true, BackupCodesLeft: 10}, "Codes": []string{"abcd-efgh"},
		}, []string{"abcd-efgh", "Резервные коды"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := adminTmpl.ExecuteTemplate(&buf, "profile-2fa", c.data); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			html := buf.String()
			for _, want := range c.want {
				if !strings.Contains(html, want) {
					t.Errorf("в HTML нет %q", want)
				}
			}
		})
	}
}

func TestParseRoleMappings(t *testing.T) {
	mappings, err := parseRoleMappings("groups = erp-buh -> Бухгалтерия\n# комментарий\ngroups = erp-admins -> Кладовщик, *admin\n")
	if err != nil {
		t.Fatalf("parseRoleMappings: %v", err)
	}
	if len(mappings) != 2 {
		t.Fatalf("разобрано правил %d, ожидалось 2: %+v", len(mappings), mappings)
	}
	if mappings[0] != (auth.OIDCRoleMapping{Claim: "groups", Value: "erp-buh", Role: "Бухгалтерия"}) {
		t.Errorf("первое правило разобрано как %+v", mappings[0])
	}
	if !mappings[1].Admin || mappings[1].Role != "Кладовщик" {
		t.Errorf("второе правило разобрано как %+v", mappings[1])
	}
	// Обратное преобразование не теряет правил.
	if got := formatRoleMappings(mappings); !strings.Contains(got, "*admin") {
		t.Errorf("формат правил потерял признак администратора: %q", got)
	}
	if _, err := parseRoleMappings("groups erp-buh Бухгалтерия"); err == nil {
		t.Error("строка без «->» принята без ошибки")
	}
}

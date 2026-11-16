package project

import "testing"

// TestAppConfig_NormalizedUIStyle — значение ui.style приводится к одному из
// известных, а опечатка не подменяется молча на classic: иначе `style: new_style`
// выглядел бы как «стиль не применился», и причину искали бы в кэше браузера.
func TestAppConfig_NormalizedUIStyle(t *testing.T) {
	cases := []struct {
		name  string
		cfg   *AppConfig
		want  string
		fails bool
	}{
		{name: "no config", cfg: nil, want: UIStyleClassic},
		{name: "no ui block", cfg: &AppConfig{}, want: UIStyleClassic},
		{name: "empty style", cfg: &AppConfig{UI: &UIConfig{}}, want: UIStyleClassic},
		{name: "classic", cfg: &AppConfig{UI: &UIConfig{Style: "classic"}}, want: UIStyleClassic},
		{name: "new-style", cfg: &AppConfig{UI: &UIConfig{Style: "new-style"}}, want: UIStyleNew},
		{name: "new-style with spaces and caps", cfg: &AppConfig{UI: &UIConfig{Style: "  NEW-Style "}}, want: UIStyleNew},
		{name: "typo", cfg: &AppConfig{UI: &UIConfig{Style: "new_style"}}, fails: true},
		{name: "unknown", cfg: &AppConfig{UI: &UIConfig{Style: "dark"}}, fails: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.NormalizedUIStyle()
			if tc.fails {
				if err == nil {
					t.Fatalf("ожидали ошибку, получили стиль %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожидали ошибку: %v", err)
			}
			if got != tc.want {
				t.Fatalf("стиль = %q, ожидали %q", got, tc.want)
			}
		})
	}
}

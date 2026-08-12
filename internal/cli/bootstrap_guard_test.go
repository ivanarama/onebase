package cli

import "testing"

// SEC-03 / issue #778: старт на не-loopback адресе без пользователей (auth
// выключен целиком) должен отклоняться, кроме явного --allow-insecure-bootstrap.
func TestBootstrapRefusal(t *testing.T) {
	cases := []struct {
		name          string
		host          string
		hasUsers      bool
		allowInsecure bool
		wantRefused   bool
	}{
		{"loopback без пользователей — ок", "127.0.0.1", false, false, false},
		{"loopback localhost — ок", "localhost", false, false, false},
		{"внешний с пользователями — ок", "0.0.0.0", true, false, false},
		{"внешний без пользователей — отказ", "0.0.0.0", false, false, true},
		{"внешний без пользователей + флаг — ок", "0.0.0.0", false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refusal := bootstrapRefusal(c.host, c.hasUsers, c.allowInsecure)
			if refused := refusal != ""; refused != c.wantRefused {
				t.Fatalf("bootstrapRefusal(%q, hasUsers=%v, allow=%v): refused=%v, ожидалось %v (%q)",
					c.host, c.hasUsers, c.allowInsecure, refused, c.wantRefused, refusal)
			}
		})
	}
}

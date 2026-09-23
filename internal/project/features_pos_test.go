package project

import "testing"

// features.pos — явный opt-in доменной возможности (issue #1331).
//
// Умолчание — «выключено»: платформа умеет РМК, но приложение, которое его не
// объявляло, не должно показывать пользователю кассовый интерфейс. Проверяем
// через LoadConfig — ту же точку входа, которой конфигурацию читает запуск.
func TestAppConfig_POSEnabled(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want bool
	}{
		{"без блока features", "name: Treasury\n", false},
		{"features без pos", "name: Treasury\nfeatures: {}\n", false},
		{"pos: false", "name: Treasury\nfeatures:\n  pos: false\n", false},
		{"pos: true", "name: Trade\nfeatures:\n  pos: true\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := LoadConfig(writeAppConfig(t, c.body))
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got := cfg.POSEnabled(); got != c.want {
				t.Errorf("POSEnabled() = %v, ожидалось %v", got, c.want)
			}
		})
	}
}

// nil-конфигурация встречается у запусков без app.yaml; метод обязан отвечать
// «выключено», а не падать.
func TestAppConfig_POSEnabledNil(t *testing.T) {
	var cfg *AppConfig
	if cfg.POSEnabled() {
		t.Error("nil-конфигурация не может включать РМК")
	}
}

package launcher

import "testing"

// hasFlag ищет пару «--flag value» в аргументах.
func hasFlag(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// --host обязан попадать в аргументы дочернего процесса — иначе база всегда
// слушала 127.0.0.1 и открыть её в локальную сеть через лаунчер было нельзя
// (issue #590). На коде без правки --host в аргументах не было вовсе.
func TestRunArgsPropagatesHost(t *testing.T) {
	def := runArgs(&Base{ID: "b", DBType: "sqlite", DBPath: "/tmp/x.db", Port: 8080, ConfigSource: "file", Path: "/proj"})
	if !hasFlag(def, "--host", "127.0.0.1") {
		t.Fatalf("--host 127.0.0.1 не проброшен по умолчанию: %v", def)
	}
	net := runArgs(&Base{ID: "b", DBType: "sqlite", DBPath: "/tmp/x.db", Port: 8080, ConfigSource: "file", Path: "/proj", Host: "0.0.0.0"})
	if !hasFlag(net, "--host", "0.0.0.0") {
		t.Fatalf("--host 0.0.0.0 не проброшен: %v", net)
	}
	// Остальные аргументы на месте — сборку не сломали.
	if !hasFlag(def, "--port", "8080") || !hasFlag(def, "--project", "/proj") {
		t.Fatalf("аргументы run собраны неверно: %v", def)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"":            "127.0.0.1",
		"127.0.0.1":   "127.0.0.1",
		"0.0.0.0":     "0.0.0.0",
		"  0.0.0.0  ": "0.0.0.0",
		"мусор":       "127.0.0.1",
		"192.168.0.1": "127.0.0.1", // не в белом списке → безопасный loopback
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, ожидали %q", in, got, want)
		}
	}
}

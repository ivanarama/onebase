package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Дочерний процесс для TestCompanionEnvReachesRealProcess: записывает
// полученную переменную в файл и выходит. В обычном прогоне ничего не делает.
func TestHelperCompanionEnvChild(t *testing.T) {
	out := os.Getenv("OB_COMPANION_ENV_OUT")
	if out == "" {
		t.Skip("вспомогательный процесс")
	}
	_ = os.WriteFile(out, []byte(os.Getenv("CALLISTA_OPERATOR_API_URL")+"|"+os.Getenv("PATH")), 0o600)
}

// env манифеста комплекта доходит до НАСТОЯЩЕГО запущенного процесса (штатный
// systemCompanionRuntime.start), окружение лаунчера при этом сохраняется.
// Handoff Callista: CALLISTA_OPERATOR_API_URL закрепляет адрес сервера в клиенте.
func TestCompanionEnvReachesRealProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "env.txt")
	wait, err := systemCompanionRuntime.start(Companion{
		Key:  "callista",
		Path: self,
		Args: []string{"-test.run=^TestHelperCompanionEnvChild$"},
		Env: map[string]string{
			"CALLISTA_OPERATOR_API_URL": "https://cc.icecorp.ru:8445",
			"OB_COMPANION_ENV_OUT":      out,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	url, path, _ := strings.Cut(string(got), "|")
	if url != "https://cc.icecorp.ru:8445" {
		t.Fatalf("переменная не дошла до процесса: %q", got)
	}
	if path == "" {
		t.Fatal("окружение лаунчера (PATH) потерялось")
	}
}

// env читается из манифеста, неверное имя отвергает манифест целиком, путь
// станции из ibases.yaml сохраняет переменные комплекта.
func TestCompanionManifestEnv(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "companions:\n  callista:\n    exec: c/callista.exe\n    env:\n      CALLISTA_OPERATOR_API_URL: https://cc.icecorp.ru:8445\n")
	list, err := loadCompanionManifest(dir)
	if err != nil || len(list) != 1 || list[0].Env["CALLISTA_OPERATOR_API_URL"] != "https://cc.icecorp.ru:8445" {
		t.Fatalf("манифест: %+v %v", list, err)
	}
	merged := mergeCompanions(list, []Companion{{Key: "callista", Path: `D:\Телефония\callista.exe`}})
	if len(merged) != 1 || merged[0].Path != `D:\Телефония\callista.exe` || merged[0].Env["CALLISTA_OPERATOR_API_URL"] == "" {
		t.Fatalf("путь станции потерял переменные комплекта: %+v", merged)
	}
	for _, bad := range []string{"1BAD: x", "\"A=B\": x", "\"A B\": x"} {
		writeManifest(t, dir, "companions:\n  callista:\n    exec: c/callista.exe\n    env:\n      "+bad+"\n")
		if _, err := loadCompanionManifest(dir); err == nil || !strings.Contains(err.Error(), "env") {
			t.Errorf("%s: ждали отказ манифеста, получили %v", bad, err)
		}
	}
	got := companionEnv([]string{"Path=C:\\Windows", "callista_operator_api_url=старое"}, map[string]string{"CALLISTA_OPERATOR_API_URL": "новое"})
	if strings.Join(got, ";") != "Path=C:\\Windows;CALLISTA_OPERATOR_API_URL=новое" {
		t.Fatalf("наложение окружения: %v", got)
	}
}

package launcher

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/selfupdate"
)

func renderUpdatesPage(t *testing.T, vm updatesVM) string {
	t.Helper()
	var buf bytes.Buffer
	data := map[string]any{"Title": "test", "Lang": "ru", "U": vm}
	if err := tmpl.ExecuteTemplate(&buf, "page-updates", data); err != nil {
		t.Fatalf("ExecuteTemplate page-updates: %v", err)
	}
	return buf.String()
}

func renderIndexWithUpdate(t *testing.T, vm updatesVM) string {
	t.Helper()
	var buf bytes.Buffer
	data := map[string]any{
		"Title": "test", "Lang": "ru",
		"Bases": []*baseVM{}, "Selected": nil, "BaseURL": "",
		"Update": vm,
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-index", data); err != nil {
		t.Fatalf("ExecuteTemplate page-index: %v", err)
	}
	return buf.String()
}

// Отметка в шапке появляется только когда есть что предложить: на канале build
// сборки выходят по нескольку раз в день, и постоянно светящийся бейдж
// перестал бы что-либо значить.
func TestUpdatesBadge_OnlyWhenAvailable(t *testing.T) {
	same := updatesVM{Enabled: true, Current: "build-672", LatestTag: "build-672"}
	html := renderIndexWithUpdate(t, same)
	if strings.Contains(html, `class="tbtn upd-badge"`) {
		t.Error("бейдж нарисован, хотя установлена актуальная версия")
	}
	if !strings.Contains(html, `href="/updates"`) {
		t.Error("ссылка на страницу обновления должна быть доступна всегда")
	}

	avail := updatesVM{Enabled: true, Current: "build-660", LatestTag: "build-672", Available: true, SameScheme: true}
	html = renderIndexWithUpdate(t, avail)
	if !strings.Contains(html, `class="tbtn upd-badge"`) || !strings.Contains(html, "build-672") {
		t.Error("при доступном обновлении в шапке должен быть бейдж с номером сборки")
	}
}

// Политика администратора убирает средства обновления целиком — и из шапки,
// и со страницы.
func TestUpdates_PolicyDisabledHidesEverything(t *testing.T) {
	vm := updatesVM{Enabled: false, Current: "build-660", LatestTag: "build-672", Available: true, BinDir: `C:\Program Files\onebase`}

	if html := renderIndexWithUpdate(t, vm); strings.Contains(html, `href="/updates"`) {
		t.Error("при запрете политикой ссылки на обновление в шапке быть не должно")
	}

	page := renderUpdatesPage(t, vm)
	if !strings.Contains(page, "политикой администратора") {
		t.Error("страница должна объяснять, что обновление запрещено политикой")
	}
	if strings.Contains(page, "/updates/check") || strings.Contains(page, "/updates/apply") {
		t.Error("при запрете политикой кнопок действий быть не должно")
	}
}

// Нет прав на каталог бинаря — обновляться нельзя, и это надо сказать словами,
// а не молча спрятать кнопку.
func TestUpdatesPage_NoWriteAccessExplained(t *testing.T) {
	vm := updatesVM{
		Enabled: true, NetAllowed: true, CanWrite: false,
		BinDir:  `C:\Program Files\onebase`,
		Current: "build-660", LatestTag: "build-672", Available: true, SameScheme: true,
		StagedTag: "build-672",
	}
	page := renderUpdatesPage(t, vm)
	if !strings.Contains(page, "Нет прав на запись") {
		t.Error("страница должна объяснять отсутствие прав на каталог платформы")
	}
	if !strings.Contains(page, `C:\Program Files\onebase`) {
		t.Error("в объяснении должен быть путь, куда нет доступа")
	}
	if strings.Contains(page, `return updApply()`) {
		t.Error("кнопки применения без прав на запись быть не должно")
	}
}

// Кнопка применения появляется только тогда, когда обновление уже скачано и
// проверено: применять нечего, пока файлов нет.
func TestUpdatesPage_ApplyOnlyWhenStaged(t *testing.T) {
	base := updatesVM{
		Enabled: true, NetAllowed: true, CanWrite: true,
		Current: "build-660", LatestTag: "build-672", Available: true, SameScheme: true,
	}
	page := renderUpdatesPage(t, base)
	if strings.Contains(page, `return updApply()`) {
		t.Error("до скачивания применять нечего")
	}
	if !strings.Contains(page, "/updates/download'") {
		t.Error("должна быть кнопка скачивания")
	}

	base.StagedTag = "build-672"
	base.RunningCount = 2
	page = renderUpdatesPage(t, base)
	if !strings.Contains(page, `return updApply()`) {
		t.Error("скачанное обновление должно предлагаться к применению")
	}
	// Пользователь обязан заранее видеть цену действия.
	if !strings.Contains(page, "остановит запущенные базы") || !strings.Contains(page, "(2)") {
		t.Error("нет предупреждения о том, сколько баз будет остановлено")
	}
	if !strings.Contains(page, "обратно не мигрируются") {
		t.Error("нет предупреждения об ограничении отката")
	}
}

func TestUpdatesPage_BackupChoiceCoversApplyAndRollback(t *testing.T) {
	apply := updatesVM{
		Enabled: true, CanWrite: true,
		Current: "build-930", StagedTag: "build-931",
	}
	applyPage := renderUpdatesPage(t, apply)
	if got := strings.Count(applyPage, `id="upd-backup"`); got != 1 {
		t.Fatalf("apply page backup checkbox count = %d, want 1", got)
	}
	if !strings.Contains(applyPage, `id="upd-backup" checked`) {
		t.Fatal("backup checkbox must be enabled by default for apply")
	}

	rollback := updatesVM{
		Enabled: true, CanWrite: true,
		Current: "build-931", PrevTag: "build-930",
	}
	rollbackPage := renderUpdatesPage(t, rollback)
	if strings.Contains(rollbackPage, `return updApply()`) {
		t.Fatal("rollback-only page unexpectedly offers apply")
	}
	if got := strings.Count(rollbackPage, `id="upd-backup"`); got != 1 {
		t.Fatalf("rollback-only page backup checkbox count = %d, want 1", got)
	}
	if !strings.Contains(rollbackPage, `id="upd-backup" checked`) {
		t.Fatal("backup checkbox must be enabled by default for rollback")
	}

	start := strings.Index(rollbackPage, "function updRollback(tag)")
	if start < 0 {
		t.Fatal("rendered page has no rollback JavaScript")
	}
	rollbackJS := rollbackPage[start:]
	for _, want := range []string{
		"var backup = updBackupEnabled();",
		"var url = '/updates/rollback' + (backup ? '?backup=1' : '');",
		"return updPost(url, busy, function(){",
	} {
		if !strings.Contains(rollbackJS, want) {
			t.Errorf("rollback JavaScript does not use shared backup choice: missing %q", want)
		}
	}

	noVersionChange := updatesVM{Enabled: true, CanWrite: true, Current: "build-931"}
	if page := renderUpdatesPage(t, noVersionChange); strings.Contains(page, `id="upd-backup"`) {
		t.Fatal("backup checkbox is visible when neither apply nor rollback is available")
	}
}

// Переключатель канала прячется, когда канал задан администратором.
func TestUpdatesPage_ChannelLocked(t *testing.T) {
	vm := updatesVM{Enabled: true, NetAllowed: true, CanWrite: true, Current: "v0.9.8", Channel: "stable", ChannelLocked: true}
	page := renderUpdatesPage(t, vm)
	if strings.Contains(page, `onclick="setChannel(`) {
		t.Error("при зафиксированном канале переключателя быть не должно")
	}
	if !strings.Contains(page, "задан администратором") {
		t.Error("надо сказать, что канал задан администратором")
	}
}

// Переключение канала между схемами версий формулируется честно: это не
// «более новая сборка», а другой канал.
func TestUpdatesPage_CrossChannelWording(t *testing.T) {
	vm := updatesVM{
		Enabled: true, NetAllowed: true, CanWrite: true,
		Current: "build-672", Channel: string(selfupdate.ChannelStable),
		LatestTag: "v0.9.9", Available: true, SameScheme: false,
	}
	page := renderUpdatesPage(t, vm)
	if !strings.Contains(page, "переключение канала") {
		t.Error("для другой схемы версий формулировка должна отличаться от «доступна новая версия»")
	}
}

// Неудачную проверку показываем строкой, а не тишиной: офлайн-машина должна
// понимать, почему сведений нет.
func TestUpdatesPage_CheckErrorShown(t *testing.T) {
	vm := updatesVM{Enabled: true, NetAllowed: true, CanWrite: true, Current: "build-660", CheckError: "нет сети"}
	page := renderUpdatesPage(t, vm)
	if !strings.Contains(page, "нет сети") {
		t.Error("ошибка проверки должна быть видна пользователю")
	}
}

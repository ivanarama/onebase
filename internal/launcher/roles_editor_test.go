package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// Роль в том виде, в каком её пишут руками: помимо матричных секций — права на
// обработки, построчный доступ (план 79), маскирование ПДн (план 88),
// операция disclose, комментарии и остаточное право на удалённый справочник.
const editorRoleYAML = `name: Оператор
description: Оператор КЦ
permissions:
  catalogs:
    # Клиент виден оператору целиком, телефон — под маской.
    Клиент: [read, write, disclose]
    ЕдиницыИзмерения: [read, write]
  documents:
    Задача: [read, write]
  row_access:
    documents:
      Задача:
        read:
          any:
            - { field: Исполнитель, op: eq, value: { user: login } }
  processors:
    ЗагрузкаЦен: [run]
  field_access:
    catalogs:
      Клиент:
        Телефон: { read: mask_tail, keep: 4 }
`

func roleEditorBase(t *testing.T, roleYAML string) (*handler, string) {
	t.Helper()
	ctx := context.Background()
	projDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "roles.db")

	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.NewRepo(db).EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	if err := os.MkdirAll(filepath.Join(projDir, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if roleYAML != "" {
		if err := os.WriteFile(filepath.Join(projDir, "roles", "оператор.yaml"), []byte(roleYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	b := &Base{ID: "roles-editor", Name: "roles-editor", ConfigSource: "file", Path: projDir, DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{b}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseAuthPools)
	return &handler{store: store, runner: NewRunner()}, projDir
}

// saveRoleFromMatrix отправляет то же, что отправила бы матрица конфигуратора:
// отмеченные триплеты «вид|объект|операция».
func saveRoleFromMatrix(t *testing.T, h *handler, name, origName string, perms ...string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("name", name)
	form.Set("orig_name", origName)
	form.Set("description", "Оператор КЦ")
	for _, p := range perms {
		form.Add("perm", p)
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/roles-editor/configurator/admin/roles/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, "roles-editor")
	rec := httptest.NewRecorder()
	h.cfgAdminRoleSave(rec, req)
	return rec
}

// Матрица конфигуратора показывает пять секций прав и по нескольку операций в
// каждой. Всё, чего в ней нет, сохранение обязано перенести из файла как есть:
// иначе один клик по чекбоксу снимал бы построчный доступ и маскирование ПДн —
// молча, без единого следа в интерфейсе.
func TestRoleSaveKeepsSectionsOutsideMatrix(t *testing.T) {
	h, projDir := roleEditorBase(t, editorRoleYAML)

	rec := saveRoleFromMatrix(t, h, "Оператор", "Оператор",
		"catalog|Клиент|read", "catalog|Клиент|write", "document|Задача|read")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело %q", rec.Code, rec.Body.String())
	}

	saved, err := os.ReadFile(filepath.Join(projDir, "roles", "оператор.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := auth.ParseRole(saved)
	if err != nil {
		t.Fatalf("сохранённый YAML не разбирается: %v\n%s", err, saved)
	}

	if got := role.Permissions.Processors["ЗагрузкаЦен"]; len(got) != 1 || got[0] != "run" {
		t.Errorf("права на обработки потеряны: %v\n%s", role.Permissions.Processors, saved)
	}
	if role.Permissions.RowAccess.IsZero() {
		t.Errorf("построчный доступ (row_access) потерян:\n%s", saved)
	}
	if role.Permissions.FieldAccess.IsZero() {
		t.Errorf("маскирование полей (field_access) потеряно:\n%s", saved)
	}
	// disclose матрица не показывает — значит и снимать его она не вправе.
	if !auth.PermissionHas(role.Permissions, "catalog", "Клиент", "disclose") {
		t.Errorf("право disclose снято сохранением из матрицы: %v\n%s", role.Permissions.Catalogs, saved)
	}
	if !strings.Contains(string(saved), "Клиент виден оператору целиком") {
		t.Errorf("комментарии файла роли затёрты:\n%s", saved)
	}

	// Отмеченное матрицей применяется: у Задачи снят write.
	if auth.PermissionHas(role.Permissions, "document", "Задача", "write") {
		t.Errorf("снятая в матрице операция осталась: %v", role.Permissions.Documents)
	}

	// Живая роль в _roles должна повторять файл, иначе рантайм разойдётся с
	// конфигурацией ровно на тех правах, что редактор не показывает.
	live := liveRole(t, h, "Оператор")
	if live.Permissions.RowAccess.IsZero() || live.Permissions.FieldAccess.IsZero() ||
		len(live.Permissions.Processors) == 0 {
		t.Errorf("живая роль потеряла секции вне матрицы: %+v", live.Permissions)
	}
}

// Право на удалённый объект («Проверка конфигурации» его показывает) должно
// сниматься из интерфейса: в матрице для него появляется помеченная строка, и
// снятая галочка убирает право из файла.
func TestRoleSaveRemovesPermissionForDeletedObject(t *testing.T) {
	h, projDir := roleEditorBase(t, editorRoleYAML)

	// ЕдиницыИзмерения в матрице не отмечены — справочника в конфигурации нет.
	rec := saveRoleFromMatrix(t, h, "Оператор", "Оператор",
		"catalog|Клиент|read", "catalog|Клиент|write", "document|Задача|read")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело %q", rec.Code, rec.Body.String())
	}

	saved, err := os.ReadFile(filepath.Join(projDir, "roles", "оператор.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "ЕдиницыИзмерения") {
		t.Errorf("остаточное право на удалённый справочник не снялось:\n%s", saved)
	}
}

// Строка удалённого объекта обязана быть в матрице — иначе снять право нечем:
// чекбокса нет, и сохранение проходит мимо него.
func TestRoleMatrixShowsPermissionsForMissingObjects(t *testing.T) {
	data := &configuratorData{Catalogs: []cfgEntity{{Name: "Клиент"}}}
	roles := []*auth.Role{{
		Name: "Оператор",
		Permissions: auth.Permission{
			Catalogs:  map[string][]string{"Клиент": {"read"}, "ЕдиницыИзмерения": {"read", "write"}},
			Documents: map[string][]string{"УдалённыйДокумент": {"read"}},
		},
	}}

	stale := staleRolePerms(roles, data)
	if got := stale["catalog"]; len(got) != 1 || got[0] != "ЕдиницыИзмерения" {
		t.Fatalf("stale[catalog] = %v", got)
	}
	if got := stale["document"]; len(got) != 1 || got[0] != "УдалённыйДокумент" {
		t.Fatalf("stale[document] = %v", got)
	}

	html := roleMatrixHTML(data, stale)
	for _, want := range []string{
		`value="catalog|ЕдиницыИзмерения|read"`,
		`value="catalog|ЕдиницыИзмерения|write"`,
		`value="document|УдалённыйДокумент|read"`,
		"нет в конфигурации",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в матрице нет %q", want)
		}
	}
}

// Инвариант матрицы: у каждого права роли по управляемой операции есть свой
// чекбокс. Право без чекбокса нельзя ни увидеть, ни снять — именно так и вели
// себя права на удалённый справочник: «Проверка конфигурации» их показывала, а
// редактор роли — нет.
//
// Операции вне матрицы (disclose плана 88) в инвариант не входят осознанно:
// редактор их не показывает и потому не трогает, они переносятся из файла как
// есть (TestRoleSaveKeepsSectionsOutsideMatrix).
func TestRoleMatrixCoversEveryManagedPermission(t *testing.T) {
	data := &configuratorData{
		Catalogs: []cfgEntity{{Name: "Клиент"}},
		Docs:     []cfgEntity{{Name: "Задача"}},
	}
	role := &auth.Role{Name: "Оператор", Permissions: auth.Permission{
		Catalogs:  map[string][]string{"Клиент": {"read"}, "ЕдиницыИзмерения": {"read", "write"}},
		Documents: map[string][]string{"Задача": {"read"}, "УдалённыйДокумент": {"post"}},
	}}

	managed := map[string]bool{}
	for _, sec := range rolePermSections {
		for _, op := range sec.Ops {
			managed[sec.Kind+"|"+op.Op] = true
		}
	}
	html := roleMatrixHTML(data, staleRolePerms([]*auth.Role{role}, data))
	for _, triplet := range permTriplets(role.Permissions) {
		parts := strings.SplitN(triplet, "|", 3)
		if len(parts) != 3 || !managed[parts[0]+"|"+parts[2]] {
			continue
		}
		if !strings.Contains(html, `value="`+escHTML(triplet)+`"`) {
			t.Errorf("право %q без чекбокса в матрице — снять его из интерфейса нечем", triplet)
		}
	}
}

// Остаточное право снимается галочкой, а не самим фактом сохранения: роль,
// сохранённая без изменений, обязана остаться прежней. Иначе строка «нет в
// конфигурации» была бы декорацией, а правку делало бы сохранение вслепую.
func TestRoleSaveKeepsStalePermissionWhenLeftChecked(t *testing.T) {
	h, projDir := roleEditorBase(t, editorRoleYAML)

	rec := saveRoleFromMatrix(t, h, "Оператор", "Оператор",
		"catalog|Клиент|read", "catalog|Клиент|write",
		"catalog|ЕдиницыИзмерения|read", "catalog|ЕдиницыИзмерения|write",
		"document|Задача|read", "document|Задача|write")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело %q", rec.Code, rec.Body.String())
	}
	saved, err := os.ReadFile(filepath.Join(projDir, "roles", "оператор.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := auth.ParseRole(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.PermissionHas(role.Permissions, "catalog", "ЕдиницыИзмерения", "write") {
		t.Errorf("оставленное отмеченным право снялось само:\n%s", saved)
	}
}

// Пока конфигурация не прочитана, объектов не видно вовсе — пометить в этот
// момент все права роли как «нет в конфигурации» значит предложить админу снять
// рабочие права.
func TestRoleMatrixKeepsSilentWhenConfigUnavailable(t *testing.T) {
	data := &configuratorData{Error: "Нет подключения к БД"}
	roles := []*auth.Role{{
		Name:        "Оператор",
		Permissions: auth.Permission{Catalogs: map[string][]string{"Клиент": {"read"}}},
	}}
	if stale := staleRolePerms(roles, data); len(stale) != 0 {
		t.Fatalf("права помечены удалёнными при нечитаемой конфигурации: %v", stale)
	}
}

// Новая роль (файла ещё нет) сохраняется как раньше — регрессия на пустой вход
// хирургической правки YAML.
func TestRoleSaveCreatesNewRoleFile(t *testing.T) {
	h, projDir := roleEditorBase(t, "")

	rec := saveRoleFromMatrix(t, h, "Кладовщик", "", "catalog|Склад|read", "document|Приход|read", "document|Приход|post")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело %q", rec.Code, rec.Body.String())
	}
	saved, err := os.ReadFile(filepath.Join(projDir, "roles", nameToFilename("Кладовщик")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := auth.ParseRole(saved)
	if err != nil {
		t.Fatalf("сохранённый YAML не разбирается: %v\n%s", err, saved)
	}
	if !auth.PermissionHas(role.Permissions, "catalog", "Склад", "read") ||
		!auth.PermissionHas(role.Permissions, "document", "Приход", "post") {
		t.Errorf("права новой роли не записаны: %+v\n%s", role.Permissions, saved)
	}
}

// Секция, названная синонимом («справочники» вместо catalogs), правится на
// месте: канонический ключ рядом означал бы две секции одного вида, из которых
// матрица показывает объединение и снять права невозможно.
func TestRoleSaveEditsAliasedSectionInPlace(t *testing.T) {
	h, projDir := roleEditorBase(t, `name: Оператор
permissions:
  справочники:
    Клиент: [read, write]
`)

	rec := saveRoleFromMatrix(t, h, "Оператор", "Оператор", "catalog|Клиент|read")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело %q", rec.Code, rec.Body.String())
	}
	saved, err := os.ReadFile(filepath.Join(projDir, "roles", "оператор.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := auth.ParseRole(saved)
	if err != nil {
		t.Fatal(err)
	}
	if auth.PermissionHas(role.Permissions, "catalog", "Клиент", "write") {
		t.Errorf("снятая операция осталась в секции-синониме:\n%s", saved)
	}
	if strings.Contains(string(saved), "catalogs:") {
		t.Errorf("рядом с секцией-синонимом заведена вторая секция:\n%s", saved)
	}
}

func liveRole(t *testing.T, h *handler, name string) *auth.Role {
	t.Helper()
	b, err := h.store.Get("roles-editor")
	if err != nil {
		t.Fatal(err)
	}
	db, err := getAuthDB(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := auth.NewRepo(db).ListRoles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("роль %q не найдена в _roles", name)
	return nil
}

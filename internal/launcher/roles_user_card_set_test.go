package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestUserCardTablePartRoleEditorPreservesSource(t *testing.T) {
	source := strings.Replace(editorRoleYAML, "{ user: login }", "{ user_card: {catalog: Сотрудники, account_field: Учётка, table_part: Филиалы, column: Филиал} }", 1)
	source = strings.Replace(source, "op: eq", "op: in", 1)
	h, dir := roleEditorBase(t, source)
	rec := saveRoleFromMatrix(t, h, "Оператор", "Оператор", "document|Задача|read")
	if rec.Code != http.StatusOK {
		t.Fatalf("save role %d %s", rec.Code, rec.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "roles", "оператор.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := auth.ParseRole(b)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := role.Permissions.RowAccess.Policy("document", "Задача", "read")
	if !ok || len(p.Any) != 1 || p.Any[0].Value.UserCard == nil {
		t.Fatalf("set source lost: %s", b)
	}
	src := p.Any[0].Value.UserCard
	if src.TablePart != "Филиалы" || src.Column != "Филиал" || src.Field != "" || src.AccountField != "Учётка" {
		t.Fatalf("set source changed: %+v", src)
	}
	live := liveRole(t, h, "Оператор")
	lp, _ := live.Permissions.RowAccess.Policy("document", "Задача", "read")
	if len(lp.Any) != 1 || lp.Any[0].Value.UserCard == nil || *lp.Any[0].Value.UserCard != *src {
		t.Fatalf("stored source differs: %+v", lp)
	}
}

package auth

import (
	"strings"
	"testing"
)

// Матрица срез A плана 162: absent/null/{}/карта/compatibility дают один и тот
// же ответ во всех трёх публичных точках — User.Has, PermissionHas и
// ProcessorPermissionMode. Неявное allow-all сохраняется до среза C.
func TestProcessorPermissionModesTransitionMatrix(t *testing.T) {
	mapped := Permission{Processors: map[string][]string{"Импорт": {"run"}}}
	cases := []struct {
		name string
		p    Permission
		mode string
		want bool
	}{
		{name: "absent section stays allow-all", p: Permission{}, mode: ProcessorModeImplicitAllowAll, want: true},
		{name: "explicit null section", p: Permission{Processors: nil}, mode: ProcessorModeImplicitAllowAll, want: true},
		{name: "empty map denies", p: Permission{Processors: map[string][]string{}}, mode: ProcessorModeMap, want: false},
		{name: "map allows listed", p: mapped, mode: ProcessorModeMap, want: true},
		{name: "map denies other entity", p: mapped, mode: ProcessorModeMap, want: true},
		{name: "compat key allows", p: Permission{ProcessorsDefault: "allow"}, mode: ProcessorModeExplicitAllowAll, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProcessorPermissionMode(tc.p); got != tc.mode {
				t.Fatalf("mode = %q, want %q", got, tc.mode)
			}
			entity := "Импорт"
			if tc.name == "map denies other entity" {
				entity = "Пересчёт"
				tc.want = false
			}
			u := &User{Roles: []*Role{{Name: "Роль", Permissions: tc.p}}}
			if got := u.Has("processor", entity, "run"); got != tc.want {
				t.Fatalf("User.Has = %v, want %v", got, tc.want)
			}
			if got := PermissionHas(tc.p, "processor", entity, "run"); got != tc.want {
				t.Fatalf("PermissionHas = %v, want %v", got, tc.want)
			}
		})
	}
}

// Union ролей: одна роль с {} не запрещает обработку, разрешённую другой
// явной картой; неявная роль по-прежнему открывает всё.
func TestProcessorPermissionUnion(t *testing.T) {
	deny := &Role{Name: "Запрет", Permissions: Permission{Processors: map[string][]string{}}}
	allow := &Role{Name: "Карта", Permissions: Permission{Processors: map[string][]string{"Импорт": {"run"}}}}
	u := &User{Roles: []*Role{deny, allow}}
	if !u.Has("processor", "Импорт", "run") {
		t.Fatal("явная карта другой роли должна разрешать обработку")
	}
	if u.Has("processor", "Пересчёт", "run") {
		t.Fatal("карта разрешает только перечисленное")
	}

	legacy := &Role{Name: "Legacy"}
	u2 := &User{Roles: []*Role{deny, legacy}}
	if !u2.Has("processor", "Пересчёт", "run") {
		t.Fatal("неявная роль в переходном срезе открывает все обработки")
	}
}

func TestParseRoleProcessorsDefault(t *testing.T) {
	t.Run("valid compat role", func(t *testing.T) {
		role, err := ParseRole([]byte("name: Совместимая\npermissions:\n  processors_default: allow\n"))
		if err != nil {
			t.Fatalf("ParseRole: %v", err)
		}
		if strings.TrimSpace(role.Permissions.ProcessorsDefault) != "allow" {
			t.Fatalf("ProcessorsDefault = %q", role.Permissions.ProcessorsDefault)
		}
		if ProcessorPermissionMode(role.Permissions) != ProcessorModeExplicitAllowAll {
			t.Fatalf("mode = %q", ProcessorPermissionMode(role.Permissions))
		}
	})
	t.Run("unknown value rejected", func(t *testing.T) {
		if _, err := ParseRole([]byte("name: Роль\npermissions:\n  processors_default: deny\n")); err == nil {
			t.Fatal("ожидали отказ для processors_default: deny")
		}
	})
	t.Run("incompatible with explicit map", func(t *testing.T) {
		src := "name: Роль\npermissions:\n  processors_default: allow\n  processors:\n    Импорт: [run]\n"
		if _, err := ParseRole([]byte(src)); err == nil {
			t.Fatal("ожидали отказ: processors_default вместе с картой")
		}
	})
	t.Run("incompatible with empty map", func(t *testing.T) {
		src := "name: Роль\npermissions:\n  processors_default: allow\n  processors: {}\n"
		if _, err := ParseRole([]byte(src)); err == nil {
			t.Fatal("ожидали отказ: processors_default вместе с {}")
		}
	})
}

// Compatibility-ключ обязан переживать представление прав в _roles: старые
// бинари его игнорируют, новые после SyncRoles должны видеть снова.
func TestProcessorsDefaultJSONRoundTrip(t *testing.T) {
	p := Permission{ProcessorsDefault: "allow"}
	data, err := marshalPermissions(p)
	if err != nil {
		t.Fatalf("marshalPermissions: %v", err)
	}
	if !strings.Contains(data, `"processors_default":"allow"`) {
		t.Fatalf("JSON потерял processors_default: %s", data)
	}
	back := unmarshalPermissions([]byte(data))
	if got := ProcessorPermissionMode(back); got != ProcessorModeExplicitAllowAll {
		t.Fatalf("mode after round-trip = %q", got)
	}
}

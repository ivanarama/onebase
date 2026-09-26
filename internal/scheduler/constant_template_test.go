package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Подстановка {{constant:Имя}} отдаёт значение константы С ЕГО ТИПОМ: параметр
// отчёта должен получить bool, а не строку «true», иначе условие в запросе
// сработает не так, как настройка учётной политики (#1433).
func TestConstantTemplate_TypedValue(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	res := ConstantResolver(func(name string) (any, error) {
		if name != "УчетСНДС" {
			t.Fatalf("резолвер получил неожиданное имя %q", name)
		}
		return true, nil
	})
	v, err := resolveTemplate("{{constant:УчетСНДС}}", now, res)
	if err != nil {
		t.Fatalf("подстановка: %v", err)
	}
	if b, ok := v.(bool); !ok || !b {
		t.Errorf("значение = %#v, ожидался bool true", v)
	}
}

// Fail closed. Каждый из этих случаев раньше молча вернул бы сам текст шаблона,
// и в параметр запроса ушла бы строка «{{constant:…}}» — отчёт посчитал бы не то
// и не сказал бы об этом ни слова.
func TestConstantTemplate_FailsClosed(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name, tmpl, want string
		res              ConstantResolver
	}{
		{
			name: "источник констант недоступен",
			tmpl: "{{constant:УчетСНДС}}",
			want: "константы недоступны",
			res:  nil,
		},
		{
			name: "имя не указано",
			tmpl: "{{constant:}}",
			want: "не указано имя",
			res:  ConstantResolver(func(string) (any, error) { return nil, nil }),
		},
		{
			name: "резолвер отказал",
			tmpl: "{{constant:НетТакой}}",
			want: "НетТакой",
			res: ConstantResolver(func(name string) (any, error) {
				return nil, fmt.Errorf("константа «%s» не объявлена в конфигурации", name)
			}),
		},
		{
			name: "преобразование не к дате",
			tmpl: "{{constant:УчетСНДС|minus_days:1}}",
			want: "применимо только к дате",
			res:  ConstantResolver(func(string) (any, error) { return true, nil }),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveTemplate(tc.tmpl, now, tc.res)
			if err == nil {
				t.Fatal("ожидалась ошибка, подстановка прошла молча")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("сообщение %q не содержит %q", err.Error(), tc.want)
			}
		})
	}
}

// Дату-константу можно продолжить обычным конвейером сдвигов: иначе пришлось бы
// заводить отдельную константу на каждую границу периода.
func TestConstantTemplate_DateTransform(t *testing.T) {
	base := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	res := ConstantResolver(func(string) (any, error) { return base, nil })
	v, err := resolveTemplate("{{constant:НачалоУчета|minus_days:5}}", time.Now(), res)
	if err != nil {
		t.Fatalf("подстановка: %v", err)
	}
	got, ok := v.(time.Time)
	if !ok || !got.Equal(base.AddDate(0, 0, -5)) {
		t.Errorf("значение = %#v, ожидалось 2026-04-30", v)
	}
}

// NewConstantResolver поверх настоящей базы: объявленная константа читается,
// незаявленная отказывает с именем, по которому искать опечатку.
func TestNewConstantResolver_OverDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	константа := &metadata.Constant{Name: "УчетСНДС", Type: "boolean"}
	if err := db.MigrateConstants(ctx, []*metadata.Constant{константа}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConstant(ctx, "УчетСНДС", true); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Constants: []*metadata.Constant{константа}})

	res := NewConstantResolver(ctx, db, reg)
	if res == nil {
		t.Fatal("резолвер не собрался при непустой базе")
	}
	v, err := res("УчетСНДС")
	if err != nil {
		t.Fatalf("чтение константы: %v", err)
	}
	if b, ok := v.(bool); !ok || !b {
		t.Errorf("значение = %#v, ожидался bool true", v)
	}

	if _, err := res("УчетСНДC_опечатка"); err == nil {
		t.Error("незаявленная константа прошла молча")
	} else if !strings.Contains(err.Error(), "не объявлена") {
		t.Errorf("сообщение не объясняет причину: %v", err)
	}

	if NewConstantResolver(ctx, nil, reg) != nil {
		t.Error("без базы резолвер обязан быть nil — тогда подстановка отказывает явно")
	}
}

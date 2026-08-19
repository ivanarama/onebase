package storage_test

// Административные решения о включённости регламентных заданий (#991):
// три-состояние в _settings поверх YAML-дефолта.
//
// Матричный тест, а не юнит на SQLite:семантика LIKE по префиксу ключа и
// DELETE/UPSERT должны совпадать на обоих диалектах — правило dbtest из
// CLAUDE.md (повод — #607). Читает эти ключи и планировщик (на каждом тике),
// и страница списка, и CLI: расхождение диалектов проявилось бы сразу в
// прикладном коде.

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestScheduledEnabled_ТриСостоянияRoundTrip(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		// Решения нет — действует конфигурационный дефолт.
		if _, ok, err := db.GetScheduledEnabled(ctx, "ОбменДанными"); err != nil {
			t.Fatalf("GetScheduledEnabled без ключа: %v", err)
		} else if ok {
			t.Fatal("ok=true при отсутствующем решении")
		}

		// Администратор включил.
		if err := db.SaveScheduledEnabled(ctx, "ОбменДанными", true); err != nil {
			t.Fatalf("SaveScheduledEnabled(true): %v", err)
		}
		if on, ok, err := db.GetScheduledEnabled(ctx, "ОбменДанными"); err != nil {
			t.Fatalf("GetScheduledEnabled после включения: %v", err)
		} else if !ok || !on {
			t.Fatalf("после Save(true): on=%v ok=%v, ожидалось true/true", on, ok)
		}

		// Администратор выключил — тот же ключ, другое значение.
		if err := db.SaveScheduledEnabled(ctx, "ОбменДанными", false); err != nil {
			t.Fatalf("SaveScheduledEnabled(false): %v", err)
		}
		if on, ok, err := db.GetScheduledEnabled(ctx, "ОбменДанными"); err != nil {
			t.Fatalf("GetScheduledEnabled после выключения: %v", err)
		} else if !ok || on {
			t.Fatalf("после Save(false): on=%v ok=%v, ожидалось false/true", on, ok)
		}

		// Reset: решения снова нет.
		if err := db.DeleteScheduledEnabled(ctx, "ОбменДанными"); err != nil {
			t.Fatalf("DeleteScheduledEnabled: %v", err)
		}
		if _, ok, err := db.GetScheduledEnabled(ctx, "ОбменДанными"); err != nil {
			t.Fatalf("GetScheduledEnabled после удаления: %v", err)
		} else if ok {
			t.Fatal("ok=true после удаления решения")
		}
	})
}

func TestScheduledEnabled_НормализуетИмяЗадания(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		if err := db.SaveScheduledEnabled(ctx, "ОбменДанными", true); err != nil {
			t.Fatalf("SaveScheduledEnabled: %v", err)
		}

		// Регистр и обрезка пробелов по краям — как jobKey планировщика:
		// вызовы из разных мест не должны расходиться в написании имени.
		// (Пробел внутри имени — часть имени, trim его не трогает.)
		on, ok, err := db.GetScheduledEnabled(ctx, "  ОБМЕНДАННЫМИ ")
		if err != nil {
			t.Fatalf("GetScheduledEnabled в другом регистре: %v", err)
		}
		if !ok || !on {
			t.Fatalf("решение не найдено в другом регистре: on=%v ok=%v", on, ok)
		}

		// И reset в другом регистре убирает тот же ключ.
		if err := db.DeleteScheduledEnabled(ctx, "обменданными"); err != nil {
			t.Fatalf("DeleteScheduledEnabled в другом регистре: %v", err)
		}
		if _, ok, _ := db.GetScheduledEnabled(ctx, "ОбменДанными"); ok {
			t.Fatal("решение пережило удаление в другом регистре")
		}
	})
}

func TestScheduledEnabledOverrides_ТолькоСвоёСемейство(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		if err := db.SaveScheduledEnabled(ctx, "ТелеграмПоллинг", true); err != nil {
			t.Fatalf("SaveScheduledEnabled(ТелеграмПоллинг): %v", err)
		}
		if err := db.SaveScheduledEnabled(ctx, "АвтоБэкап", false); err != nil {
			t.Fatalf("SaveScheduledEnabled(АвтоБэкап): %v", err)
		}
		// Чужой ключ и мусорное значение в своём семействе не должны
		// попадать в карту.
		if err := db.SaveSetting(ctx, "net.enabled", "1"); err != nil {
			t.Fatalf("SaveSetting(net.enabled): %v", err)
		}
		if err := db.SaveSetting(ctx, scheduledEnabledKeyForTest("Битое"), "да"); err != nil {
			t.Fatalf("SaveSetting(битое значение): %v", err)
		}

		overrides, err := db.ScheduledEnabledOverrides(ctx)
		if err != nil {
			t.Fatalf("ScheduledEnabledOverrides: %v", err)
		}
		if len(overrides) != 2 {
			t.Fatalf("в карте %d записей, ожидалось 2 (без net.enabled и без битого): %v",
				len(overrides), overrides)
		}
		if !overrides["телеграмполлинг"] {
			t.Errorf("телеграмполлинг: on=false, ожидалось true")
		}
		if overrides["автобэкап"] {
			t.Errorf("автобэкап: on=true, ожидалось false")
		}
	})
}

func TestDeleteScheduledEnabled_БезКлючаНеОшибка(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if err := db.DeleteScheduledEnabled(context.Background(), "НезаписанноеЗадание"); err != nil {
			t.Fatalf("DeleteScheduledEnabled без ключа: %v", err)
		}
	})
}

// scheduledEnabledKeyForTest пишет в _settings сырым ключом семейства —
// единственный способ посеять битое значение, которое код записи не создаёт.
func scheduledEnabledKeyForTest(name string) string {
	return "scheduled.enabled." + name
}

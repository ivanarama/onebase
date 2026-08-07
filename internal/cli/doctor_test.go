package cli

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// #610: --forget-document обязан отказать, если названный тип регистратора всё
// ещё есть в конфигурации как документ. Удаление движений необратимо, а следом
// пересчитываются итоги — опечатка в имени живого документа стёрла бы его
// историю без следа. Осиротевшими движения становятся только когда документ
// убран из конфигурации.
func TestForgetTypesInConfig(t *testing.T) {
	proj := &project.Project{
		Entities: []*metadata.Entity{
			{Name: "Реализация", Kind: metadata.KindDocument},
			{Name: "Номенклатура", Kind: metadata.KindCatalog},
		},
	}

	// Живой документ — отказ (в т.ч. при отличии только регистром).
	for _, name := range []string{"Реализация", "реализация", "  Реализация  "} {
		if bad := forgetTypesInConfig(proj, []string{name}); len(bad) != 1 {
			t.Errorf("%q: документ есть в конфигурации, ожидался отказ, получили %v", name, bad)
		}
	}

	// Справочник документом-регистратором не является — под forget не подпадает.
	if bad := forgetTypesInConfig(proj, []string{"Номенклатура"}); len(bad) != 0 {
		t.Errorf("справочник ошибочно принят за документ: %v", bad)
	}

	// Действительно выбывший тип — пропускаем к удалению.
	if bad := forgetTypesInConfig(proj, []string{"СовсемУбранныйДокумент"}); len(bad) != 0 {
		t.Errorf("выбывший документ не должен блокироваться: %v", bad)
	}

	// Смесь: часть живая — возвращаем только живые.
	bad := forgetTypesInConfig(proj, []string{"Реализация", "СовсемУбранныйДокумент", ""})
	if len(bad) != 1 || bad[0] != "Реализация" {
		t.Errorf("ожидался только живой документ, получили %v", bad)
	}
}

package ui

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// eq_or_empty с глубоким источником (план 183, срез B1) — через публичный
// маршрут подбора. «Свои плюс общие» работает, только когда посредник открыт и
// его реквизит заполнен. Закрытый строковым доступом, несуществующий, скрытый
// полевой политикой или пустой реквизит посредника — fail-closed: пустая
// выдача, а не «только общие». Иначе пустой список и список общих записей
// различали бы для пользователя «реквизита нет» и «реквизит закрыт».
func TestRefOptionsEqualOrEmptyDeepSourceKeepsIntermediateClosed(t *testing.T) {
	f := newDeepChoiceFixture(t)
	f.owner.Forms[0].Elements[1].ChoiceFilter = []metadata.FormChoiceCondition{{
		Field: "Группа", Op: metadata.FormChoiceOpEqualOrEmpty,
		From: "Объект.Направление.ГруппаНеисправностей",
	}}
	if err := f.server.store.Upsert(context.Background(), f.target.Name, deepChoiceUUID(0x30, 9),
		map[string]any{"Наименование": "Общая", "Группа": ""}, f.target); err != nil {
		t.Fatalf("seed common fault: %v", err)
	}
	user := deepChoiceUser(nil)
	sorted := func(labels []string) string {
		sort.Strings(labels)
		return strings.Join(labels, ", ")
	}

	t.Run("открытый посредник: свои плюс общие", func(t *testing.T) {
		if got := sorted(f.labels(t, f.serve(t, user, f.directionA.String()))); got != "Не включается, Общая, Шумит" {
			t.Fatalf("группа 1 и общие, получено %q", got)
		}
		if got := sorted(f.labels(t, f.serve(t, user, f.directionB.String()))); got != "Общая, Течёт" {
			t.Fatalf("смена ведущего поля: группа 2 и общие, получено %q", got)
		}
	})

	t.Run("пустой реквизит посредника закрыт, общие не показываются", func(t *testing.T) {
		if labels := f.labels(t, f.serve(t, user, f.directionD.String())); len(labels) != 0 {
			t.Fatalf("незаполненная группа посредника раскрыла выдачу: %v", labels)
		}
	})

	t.Run("закрытый строковым доступом посредник закрыт", func(t *testing.T) {
		if labels := f.labels(t, f.serve(t, user, f.directionC.String())); len(labels) != 0 {
			t.Fatalf("направление чужого оператора дало выдачу: %v", labels)
		}
	})

	t.Run("несуществующий посредник неотличим от закрытого", func(t *testing.T) {
		if labels := f.labels(t, f.serve(t, user, deepChoiceUUID(0x20, 99).String())); len(labels) != 0 {
			t.Fatalf("выдача по несуществующей записи: %v", labels)
		}
	})

	t.Run("скрытый полевой политикой реквизит посредника закрыт", func(t *testing.T) {
		for _, strategy := range []string{"hide", "mask_all"} {
			masked := deepChoiceUser(auth.FieldPolicies{"ГруппаНеисправностей": auth.FieldPolicy{Read: strategy}})
			if labels := f.labels(t, f.serve(t, masked, f.directionA.String())); len(labels) != 0 {
				t.Fatalf("политика %q обойдена подбором eq_or_empty: %v", strategy, labels)
			}
		}
	})

	t.Run("пустое ведущее поле — только общие, как у прямого источника", func(t *testing.T) {
		if got := sorted(f.labels(t, f.serve(t, user, ""))); got != "Общая" {
			t.Fatalf("без ведущего значения ожидались только общие, получено %q", got)
		}
	})
}

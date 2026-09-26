package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func listFilterTestServer() (*Server, *metadata.Entity) {
	сотрудники := &metadata.Entity{
		Name: "Пользователи", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "УчётнаяЗапись", Type: "reference:_users", RefEntity: "_users"}},
	}
	задача := &metadata.Entity{
		Name: "А_Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Инициатор", Type: "reference:Пользователи", RefEntity: "Пользователи"}},
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача, сотрудники}})
	return &Server{reg: reg}, задача
}

// Отбор по реквизиту СВЯЗАННОЙ записи: «Инициатор.УчётнаяЗапись = тот, кто
// смотрит» должен стать предикатом со ссылкой, а не сравнением строк.
func TestListFilterPredicate_RefAttribute(t *testing.T) {
	s, задача := listFilterTestServer()
	user := &auth.User{ID: "u-1", Login: "оператор"}
	pred, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Инициатор.УчётнаяЗапись", Value: metadata.ListFilterCurrentUser}, user)
	if !ok {
		t.Fatal("условие не превратилось в предикат")
	}
	if pred.Field != "Инициатор" || pred.RefEntity == nil || pred.RefEntity.Name != "Пользователи" {
		t.Fatalf("ссылка разобрана неверно: %+v", pred)
	}
	if pred.RefPredicate == nil || pred.RefPredicate.Field != "УчётнаяЗапись" || pred.RefPredicate.Value != "u-1" {
		t.Fatalf("вложенное условие неверно: %+v", pred.RefPredicate)
	}
}

// Без пользователя подставить в @ТекущийПользователь нечего: условие
// пропускается целиком, иначе список выглядел бы пустым и «сломанным».
func TestListFilterPredicate_NoUserSkipsCondition(t *testing.T) {
	s, задача := listFilterTestServer()
	if _, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Инициатор.УчётнаяЗапись", Value: metadata.ListFilterCurrentUser}, nil); ok {
		t.Fatal("условие применилось без пользователя")
	}
}

// Неизвестное поле молча не превращается в предикат: иначе список отобрал бы
// по несуществующей колонке и упал бы на SQL.
func TestListFilterPredicate_UnknownReferenceIgnored(t *testing.T) {
	s, задача := listFilterTestServer()
	if _, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Неизвестное.Поле", Value: "x"}, &auth.User{ID: "u-1"}); ok {
		t.Fatal("условие по неизвестному полюприменилось")
	}
}

// Администратор видит список целиком: правило «мои записи» на него не
// распространяется — он разбирает чужие документы.
func TestListFilterPredicate_AdminSeesEverything(t *testing.T) {
	s, задача := listFilterTestServer()
	admin := &auth.User{ID: "root", Login: "admin", IsAdmin: true}
	if _, ok := s.listFilterPredicate(задача,
		metadata.FormListCondition{Field: "Инициатор.УчётнаяЗапись", Value: metadata.ListFilterCurrentUser}, admin); ok {
		t.Fatal("отбор «мои записи» применился к администратору")
	}
}

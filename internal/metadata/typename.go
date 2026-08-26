package metadata

import "strings"

// Квалифицированные имена типов объектов конфигурации в нотации 1С. Их видит
// прикладной разработчик как результат ТипЗнч() и как аргумент Тип():
//
//	Если ТипЗнч(ДанныеЗаполнения) = Тип("ДокументСсылка.ЗаказПокупателя") Тогда
//
// Формат — публичный контракт DSL: конфигурации сравнивают с этими строками,
// поэтому менять его после выпуска дороже, чем ввести (issue #1137).
const (
	TypePrefixDocument    = "Документ"
	TypePrefixCatalog     = "Справочник"
	TypePrefixDocumentRef = "ДокументСсылка"
	TypePrefixCatalogRef  = "СправочникСсылка"
)

// ObjectTypeName — имя типа самого объекта: «Документ.РеализацияТоваров»,
// «Справочник.Номенклатура». Пустая строка, если вид или имя неизвестны —
// вызывающий подставляет свой запасной вариант.
func ObjectTypeName(kind Kind, name string) string {
	return qualifiedTypeName(kind, name, TypePrefixDocument, TypePrefixCatalog)
}

// RefTypeName — имя типа ссылки на объект: «ДокументСсылка.ЗаказПокупателя»,
// «СправочникСсылка.Номенклатура». Отличать ссылку от объекта обязательно:
// в 1С это разные типы, и хук ввода на основании получает именно объект.
func RefTypeName(kind Kind, name string) string {
	return qualifiedTypeName(kind, name, TypePrefixDocumentRef, TypePrefixCatalogRef)
}

func qualifiedTypeName(kind Kind, name, docPrefix, catPrefix string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	switch kind {
	case KindDocument:
		return docPrefix + "." + name
	case KindCatalog:
		return catPrefix + "." + name
	}
	return ""
}

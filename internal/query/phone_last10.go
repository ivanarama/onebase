package query

import (
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func (tr *translator) emitPhoneLast10() error {
	call := tr.pos - 1
	scope, ok := tr.sourceCtx.scopeIDAt(call)
	section := tr.sourceCtx.sectionAt(call)
	if !ok || (section != sectionWhere && (section != sectionFrom || tr.onSourceAliases[scope] == "")) {
		return i18nerr.New("НОРМАЛИЗОВАТЬТЕЛЕФОН допускается в условии ГДЕ или ПО")
	}

	if tr.peek(1).kind != tIdent || tr.peek(2).kind != tDot || tr.peek(3).kind != tIdent || tr.peek(4).kind != tRParen {
		return i18nerr.New("НОРМАЛИЗОВАТЬТЕЛЕФОН ожидает строковое поле в виде Алиас.Поле")
	}
	name := lowerFast(tr.peek(3).val)
	qualifier := lowerFast(tr.peek(1).val)
	typ, source, known := tr.columnSourceAt(tr.pos+3, name, qualifier)
	if !known || typ != metadata.FieldTypeString || source.tokenPos < 0 {
		return i18nerr.New("НОРМАЛИЗОВАТЬТЕЛЕФОН ожидает строковое поле в виде Алиас.Поле")
	}
	kind := upperFast(tr.tokens[source.tokenPos].val)
	entity := tr.tokens[source.tokenPos+2].val
	physical := sourcePhysicalColumns(kind, entity, tr.opts)[name]
	if physical == "" {
		return i18nerr.New("НОРМАЛИЗОВАТЬТЕЛЕФОН ожидает строковое поле в виде Алиас.Поле")
	}

	tr.emit("(" + storage.PhoneLast10SQL(source.qualifier+"."+physical) + ")")
	tr.pos += 5
	return nil
}

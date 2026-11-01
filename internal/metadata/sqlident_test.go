package metadata

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSQLIdent(t *testing.T) {
	// Короткие имена не меняются — существующие базы с ними не затронуты.
	for _, s := range []string{"", "контрагенты", "рег_остатки", strings.Repeat("я", 31) + "_"} {
		if len(s) <= MaxSQLIdentBytes && SQLIdent(s) != s {
			t.Fatalf("короткое имя %q изменено: %q", s, SQLIdent(s))
		}
	}

	a := "использоватькоэффициентизмененияценмастера"
	b := "использоватькоэффициентизмененияценвремонт"
	sa, sb := SQLIdent(a), SQLIdent(b)
	for _, s := range []string{sa, sb} {
		if len(s) > MaxSQLIdentBytes || !utf8.ValidString(s) {
			t.Fatalf("сокращённое имя длиннее предела или режет символ: %q (%d байт)", s, len(s))
		}
		if !ValidIdent(s) {
			t.Fatalf("сокращённое имя не годится как идентификатор: %q", s)
		}
	}
	// Ради этого всё и делается: обрезка PostgreSQL сводила их в одно.
	if sa == sb {
		t.Fatalf("разные имена с общим началом сокращены одинаково: %q", sa)
	}
	if SQLIdent(a) != sa {
		t.Fatal("правило не детерминировано")
	}
	if !strings.HasPrefix(sa, "использоватькоэффициент") {
		t.Fatalf("сокращённое имя потеряло узнаваемое начало: %q", sa)
	}
	// Повторное применение ничего не меняет: короткое имя уже в пределе.
	if SQLIdent(sa) != sa {
		t.Fatal("SQLIdent не идемпотентна")
	}
}

func TestLegacySQLIdents(t *testing.T) {
	if LegacySQLIdents("контрагенты") != nil {
		t.Fatal("для короткого имени прежних имён нет")
	}
	long := "использоватькоэффициентизмененияценмастера"
	got := LegacySQLIdents(long)
	if len(got) != 2 || got[0] != long {
		t.Fatalf("прежние имена: %q", got)
	}
	// Так PostgreSQL обрезал идентификатор: не длиннее 63 байт и по границе
	// символа (см. NOTICE «identifier … will be truncated to …»).
	if got[1] != "использоватькоэффициентизменени" {
		t.Fatalf("обрезка PostgreSQL воспроизведена неверно: %q", got[1])
	}
}

func TestNamingFunctionsUseSQLIdent(t *testing.T) {
	long := strings.Repeat("Длинное", 6) // 42 буквы
	f := Field{Name: long, RefEntity: "Склады"}
	for name, got := range map[string]string{
		"TableName":           TableName(long),
		"TablePartTableName":  TablePartTableName(long, "ТЧ"),
		"RegisterTableName":   RegisterTableName(long),
		"InfoRegTableName":    InfoRegTableName(long),
		"AccountRegTableName": AccountRegTableName(long),
		"ColumnName":          ColumnName(f),
	} {
		if len(got) > MaxSQLIdentBytes {
			t.Errorf("%s: %q длиннее предела", name, got)
		}
	}
	if ColumnName(f) != SQLIdent(strings.ToLower(long)+"_id") {
		t.Fatalf("ColumnName ссылки: %q", ColumnName(f))
	}
}

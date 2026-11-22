package rowsource

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
)

func compileEvidence(t *testing.T, body string) *program {
	t.Helper()
	p, err := compileProgram("Функция Build() Экспорт\n"+body+"\nВозврат Результат;\nКонецФункции", "Build")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRowSourceEvidenceRequiresExactLexicalNode(t *testing.T) {
	p := compileEvidence(t, `A="SELECT 1";B="SELECT 1";`)
	var literals []*ast.StringLit
	for node := range p.evidence.literals {
		if str, ok := node.(*ast.StringLit); ok {
			literals = append(literals, str)
		}
	}
	if len(literals) != 2 {
		t.Fatal("missing literal nodes")
	}
	a, ok := p.evidence.literal(literals[0])
	if !ok {
		t.Fatal("own lexical node rejected")
	}
	b, ok := p.evidence.literal(literals[1])
	if !ok || a.value != b.value || a.node == b.node {
		t.Fatal("equal typed bytes erased node binding")
	}
	clone := *literals[0]
	if _, ok := p.evidence.literal(&clone); ok {
		t.Fatal("foreign same-valued descriptor acquired lexical proof")
	}
	literals[0].Value = "changed"
	if _, ok := p.evidence.literal(literals[0]); ok {
		t.Fatal("mutated AST retained proof")
	}
	literals[0].Value = strings.Repeat("x", 65537)
	if _, ok := p.evidence.literal(literals[0]); ok {
		t.Fatal("oversized mutated AST acquired proof")
	}
}
func TestRowSourceEvidenceBindsModuleProfileAndTypes(t *testing.T) {
	p := compileEvidence(t, `A=0;B="0";C=Ложь;D='20261116';`)
	q := compileEvidence(t, `A=0;B="0";C=Ложь;D='20261116'; // different module`)
	if p.evidence.module == q.evidence.module || p.evidence.profile != q.evidence.profile {
		t.Fatal("module/profile binding changed incorrectly")
	}
	digests := map[string][32]byte{}
	for node := range p.evidence.literals {
		proof, ok := p.evidence.literal(node)
		if !ok {
			t.Fatal("own literal rejected")
		}
		if proof.module != p.evidence.module || proof.profile != p.evidence.profile || proof.node <= 0 {
			t.Fatal("incomplete binding")
		}
		digests[proof.typ] = proof.value
		if proof.typ == "date" && !proof.contextRequired {
			t.Fatal("date literal lost timezone dependency")
		}
	}
	if len(digests) != 4 || digests["number"] == digests["string"] || digests["number"] == digests["bool"] {
		t.Fatal("typed scalar origins collapsed")
	}
}
func TestRowSourceEvidenceQueryTextIsNotValueClassification(t *testing.T) {
	p := compileEvidence(t, `З=Новый Запрос;
 З.Текст="SELECT 1";
 С="SELECT 1";З.Текст=С;
 З.Текст="SELECT "+"1";
 З.Текст+="";`)
	if len(p.evidence.textSites) != 4 {
		t.Fatalf("sites=%d", len(p.evidence.textSites))
	}
	lexical := 0
	nodes := map[int]bool{}
	for _, site := range p.evidence.textSites {
		if nodes[site.node] {
			t.Fatal("statement identities collapsed")
		}
		nodes[site.node] = true
		if !site.receiverRequired || !site.controlRequired {
			t.Fatal("lexical text silently became protected-operation authority")
		}
		if site.literal != nil {
			lexical++
			if site.literal.node == site.node {
				t.Fatal("statement and value node identity collapsed")
			}
		}
	}
	if lexical != 1 {
		t.Fatalf("dynamic same-valued text acquired lexical evidence: count=%d", lexical)
	}
}
func TestRowSourceEvidenceSameValueFunctionAndBranchStayUnproven(t *testing.T) {
	code := `Функция TextValue() Возврат "SELECT 1"; КонецФункции
 Функция Build() Экспорт
 З=Новый Запрос;З.Текст=TextValue();
 Если Результат.Количество()>0 Тогда З.Текст="SELECT 1";КонецЕсли;
 Возврат Результат;КонецФункции`
	p, err := compileProgram(code, "Build")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.evidence.textSites) != 2 {
		t.Fatal("missing query text sites")
	}
	for stmt, site := range p.evidence.textSites {
		if _, call := stmt.Value.(*ast.CallExpr); call && site.literal != nil {
			t.Fatal("function return declared fixed literal by value")
		}
		if !site.controlRequired {
			t.Fatal("branch or helper control dependency cleared")
		}
	}
}

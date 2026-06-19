package ast

import "github.com/ivantit66/onebase/internal/dsl/token"

type Node interface{ nodeType() string }
type Stmt interface {
	Node
	stmtNode()
}
type Expr interface {
	Node
	exprNode()
}

type Program struct {
	// ModuleVars — раздел объявления переменных модуля (Перем … [Экспорт];),
	// идущий в начале модуля до процедур и функций (как в 1С). Модули объекта
	// из выгрузок 1С часто начинаются с него (issue #115).
	ModuleVars []*VarDecl
	Procedures []*ProcedureDecl
}

type ProcedureDecl struct {
	Name   token.Token
	Params []token.Token
	// Defaults параллелен Params: Defaults[i] = nil → у параметра нет дефолта,
	// Defaults[i] = expr → выражение вычисляется в caller-scope если арг не
	// передан (см.
	Defaults []Expr
	Body     []Stmt
}

type IfStmt struct {
	Cond    Expr
	Then    []Stmt
	ElseIfs []ElseIfBranch
	Else    []Stmt
}

type ElseIfBranch struct {
	Cond Expr
	Body []Stmt
}

type ExprStmt struct{ X Expr }

type AssignStmt struct {
	Target Expr
	Op     token.Type // ASSIGN (=), PLUS_ASSIGN (+=), etc.
	Value  Expr
}

// VarDecl — объявление переменных: Перем а, б, в [Экспорт];. Exported отражает
// модификатор «Экспорт» (значим для переменных модуля, см. ast.Program.ModuleVars).
type VarDecl struct {
	Names    []token.Token
	Exported bool
}

type ForEachStmt struct {
	Var        token.Token
	Collection Expr
	Body       []Stmt
}

// NumericForStmt — числовой цикл: Для i = start По end Цикл ... КонецЦикла
type NumericForStmt struct {
	Var   token.Token
	Start Expr
	End   Expr
	Body  []Stmt
}

// WhileStmt — цикл с предусловием: Пока <cond> Цикл ... КонецЦикла
type WhileStmt struct {
	Tok  token.Token
	Cond Expr
	Body []Stmt
}

// ReturnStmt — ранний выход или возврат значения: Возврат [expr];
type ReturnStmt struct {
	Tok   token.Token
	Value Expr // nil для безаргументного возврата
}

type CallExpr struct {
	Callee Expr // Ident = free function; MemberExpr = method call
	Args   []Expr
}

type MemberExpr struct {
	Object Expr
	Field  token.Token
}

type Ident struct{ Tok token.Token }

type StringLit struct {
	Tok   token.Token
	Value string
}

type NumberLit struct {
	Tok   token.Token
	Value string
}

type BinaryExpr struct {
	Left  Expr
	Op    token.Token
	Right Expr
}

// NewExpr — Новый Массив / Новый Структура("A,B", v1, v2)
type NewExpr struct {
	TypeName token.Token
	Args     []Expr
}

// IndexExpr — arr[i]
type IndexExpr struct {
	Object Expr
	Index  Expr
}

// BoolLit — Истина / Ложь
type BoolLit struct {
	Tok   token.Token
	Value bool
}

// UnaryExpr — НЕ expr
type UnaryExpr struct {
	Op      token.Token
	Operand Expr
}

// TernaryExpr — ?(cond, trueVal, falseVal)
type TernaryExpr struct {
	Tok   token.Token
	Cond  Expr
	True  Expr
	False Expr
}

// TryStmt — Попытка ... Исключение ... КонецПопытки
type TryStmt struct {
	Tok    token.Token
	Try    []Stmt
	Except []Stmt
}

// BreakStmt — Прервать / Break (loop exit)
type BreakStmt struct{ Tok token.Token }

// ContinueStmt — Продолжить / Continue (skip to next iteration)
type ContinueStmt struct{ Tok token.Token }

func (*Program) nodeType() string       { return "Program" }
func (*ProcedureDecl) nodeType() string { return "ProcedureDecl" }
func (*IfStmt) nodeType() string        { return "IfStmt" }
func (*ExprStmt) nodeType() string      { return "ExprStmt" }
func (*AssignStmt) nodeType() string    { return "AssignStmt" }
func (*VarDecl) nodeType() string       { return "VarDecl" }
func (*ForEachStmt) nodeType() string    { return "ForEachStmt" }
func (*NumericForStmt) nodeType() string { return "NumericForStmt" }
func (*WhileStmt) nodeType() string      { return "WhileStmt" }
func (*ReturnStmt) nodeType() string     { return "ReturnStmt" }
func (*CallExpr) nodeType() string      { return "CallExpr" }
func (*MemberExpr) nodeType() string    { return "MemberExpr" }
func (*Ident) nodeType() string         { return "Ident" }
func (*StringLit) nodeType() string     { return "StringLit" }
func (*NumberLit) nodeType() string     { return "NumberLit" }
func (*BinaryExpr) nodeType() string    { return "BinaryExpr" }
func (*NewExpr) nodeType() string       { return "NewExpr" }
func (*IndexExpr) nodeType() string     { return "IndexExpr" }
func (*BoolLit) nodeType() string       { return "BoolLit" }
func (*UnaryExpr) nodeType() string     { return "UnaryExpr" }
func (*TernaryExpr) nodeType() string { return "TernaryExpr" }
func (*TryStmt) nodeType() string       { return "TryStmt" }
func (*BreakStmt) nodeType() string     { return "BreakStmt" }
func (*ContinueStmt) nodeType() string  { return "ContinueStmt" }

func (*IfStmt) stmtNode()      {}
func (*ExprStmt) stmtNode()    {}
func (*AssignStmt) stmtNode()  {}
func (*VarDecl) stmtNode()     {}
func (*ForEachStmt) stmtNode()    {}
func (*NumericForStmt) stmtNode() {}
func (*WhileStmt) stmtNode()      {}
func (*ReturnStmt) stmtNode()     {}
func (*TryStmt) stmtNode()        {}
func (*BreakStmt) stmtNode()      {}
func (*ContinueStmt) stmtNode()   {}

func (*CallExpr) exprNode()   {}
func (*MemberExpr) exprNode() {}
func (*Ident) exprNode()      {}
func (*StringLit) exprNode()  {}
func (*NumberLit) exprNode()  {}
func (*BinaryExpr) exprNode() {}
func (*NewExpr) exprNode()    {}
func (*IndexExpr) exprNode()  {}
func (*BoolLit) exprNode()    {}
func (*UnaryExpr) exprNode()  {}
func (*TernaryExpr) exprNode() {}

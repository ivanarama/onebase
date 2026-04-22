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
	Procedures []*ProcedureDecl
}

type ProcedureDecl struct {
	Name token.Token
	Body []Stmt
}

type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
}

type ExprStmt struct{ X Expr }

type AssignStmt struct {
	Target Expr
	Value  Expr
}

type VarDecl struct{ Name token.Token }

type CallExpr struct {
	Callee token.Token
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

func (*Program) nodeType() string       { return "Program" }
func (*ProcedureDecl) nodeType() string { return "ProcedureDecl" }
func (*IfStmt) nodeType() string        { return "IfStmt" }
func (*ExprStmt) nodeType() string      { return "ExprStmt" }
func (*AssignStmt) nodeType() string    { return "AssignStmt" }
func (*VarDecl) nodeType() string       { return "VarDecl" }
func (*CallExpr) nodeType() string      { return "CallExpr" }
func (*MemberExpr) nodeType() string    { return "MemberExpr" }
func (*Ident) nodeType() string         { return "Ident" }
func (*StringLit) nodeType() string     { return "StringLit" }
func (*NumberLit) nodeType() string     { return "NumberLit" }
func (*BinaryExpr) nodeType() string    { return "BinaryExpr" }

func (*IfStmt) stmtNode()    {}
func (*ExprStmt) stmtNode()  {}
func (*AssignStmt) stmtNode() {}
func (*VarDecl) stmtNode()   {}

func (*CallExpr) exprNode()   {}
func (*MemberExpr) exprNode() {}
func (*Ident) exprNode()      {}
func (*StringLit) exprNode()  {}
func (*NumberLit) exprNode()  {}
func (*BinaryExpr) exprNode() {}

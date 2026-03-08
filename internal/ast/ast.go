package ast

type Statement interface {
	statementNode()
}

type Expression interface {
	exprNode()
}

type ColumnSpec struct {
	Name       string
	TypeName   string
	NotNull    bool
	PrimaryKey bool
}

type CreateTableStatement struct {
	Name    string
	Columns []*ColumnSpec
}

type DropTableStatement struct {
	Name     string
	IfExists bool
}

type InsertStatement struct {
	Table   string
	Columns []string
	Values  []Expression
}

type SelectStatement struct {
	Columns []string
	Table   string
	Where   Expression // nil means no WHERE clause
}

type SetClause struct {
	Column string
	Value  Expression
}

type UpdateStatement struct {
	Table      string
	SetClauses []*SetClause
	Where      Expression
}

type DeleteStatement struct {
	Table string
	Where Expression
}

type (
	BeginStatement    struct{}
	CommitStatement   struct{}
	RollbackStatement struct{}
)

type ColumnRef struct {
	Name string
}

type BinaryExpr struct {
	Left  Expression
	Op    string
	Right Expression
}

type IsNullExpr struct {
	Expr Expression
	Not  bool // true if IS NOT NULL
}

func (e *ColumnRef) exprNode()  {}
func (e *BinaryExpr) exprNode() {}
func (e *IsNullExpr) exprNode() {}

func (s *CreateTableStatement) statementNode() {}
func (s *DropTableStatement) statementNode()   {}
func (s *InsertStatement) statementNode()      {}
func (s *SelectStatement) statementNode()      {}
func (s *DeleteStatement) statementNode()      {}
func (s *UpdateStatement) statementNode()      {}
func (s *BeginStatement) statementNode()       {}
func (s *CommitStatement) statementNode()      {}
func (s *RollbackStatement) statementNode()    {}

type IntegerLiteral struct {
	Value int64
}

func (e *IntegerLiteral) exprNode() {}

type StringLiteral struct {
	Value string
}

func (e *StringLiteral) exprNode() {}

type BoolLiteral struct {
	Value bool
}

func (e *BoolLiteral) exprNode() {}

type NullLiteral struct{}

func (e *NullLiteral) exprNode() {}

type FloatLiteral struct {
	Value float64
}

func (e *FloatLiteral) exprNode() {}

package parser

import (
	"fmt"
	"strconv"
	"strings"

	"gopostgres/internal/ast"
)

type Parser struct {
	lexer   *Lexer
	current Token
}

func NewParser(sql string) *Parser {
	p := &Parser{lexer: NewLexer(sql)}
	p.current = p.lexer.NextToken()
	return p
}

func (p *Parser) peek() Token {
	return p.current
}

func (p *Parser) advance() Token {
	token := p.current
	p.current = p.lexer.NextToken()
	return token
}

func (p *Parser) expectKeyword(value string) error {
	if p.current.Type != TOKEN_KEYWORD || p.current.Value != value {
		return fmt.Errorf("expected %s, got %q", value, p.current.Value)
	}
	p.advance()
	return nil
}

func (p *Parser) expectToken(tokenType TokenType) (Token, error) {
	if p.current.Type != tokenType {
		return Token{}, fmt.Errorf("expected token type %d, got %q", tokenType,
			p.current.Value)
	}
	tok := p.advance()
	return tok, nil
}

func (p *Parser) Parse() (ast.Statement, error) {
	if p.current.Type == TOKEN_KEYWORD {
		switch p.current.Value {
		case "CREATE":
			return p.parseCreateTable()
		case "DROP":
			return p.parseDropTable()
		case "INSERT":
			return p.parseInsert()
		case "SELECT":
			return p.parseSelect()
		}
	}
	return nil, fmt.Errorf("unexpected token %q", p.current.Value)
}

func (p *Parser) parseCreateTable() (*ast.CreateTableStatement, error) {
	// CREATE
	if err := p.expectKeyword("CREATE"); err != nil {
		return nil, err
	}
	// TABLE
	if err := p.expectKeyword("TABLE"); err != nil {
		return nil, err
	}

	// table name
	nameTok, err := p.expectToken(TOKEN_IDENT)
	if err != nil {
		return nil, err
	}

	// (
	if _, err := p.expectToken(TOKEN_LPAREN); err != nil {
		return nil, err
	}

	// columns
	var columns []*ast.ColumnSpec
	for {
		col, err := p.parseColumnSpec()
		if err != nil {
			return nil, err
		}
		columns = append(columns, col)

		if p.peek().Type == TOKEN_COMMA {
			p.advance()
			continue
		}
		break
	}
	// )
	if _, err := p.expectToken(TOKEN_RPAREN); err != nil {
		return nil, err
	}
	// optional ;
	if p.peek().Type == TOKEN_SEMICOLON {
		p.advance()
	}

	return &ast.CreateTableStatement{
		Name:    nameTok.Value,
		Columns: columns,
	}, nil
}

func (p *Parser) parseColumnSpec() (*ast.ColumnSpec, error) {
	// column name
	nameTok, err := p.expectToken(TOKEN_IDENT)
	if err != nil {
		return nil, err
	}
	// type name — could be IDENT ("integer") or KEYWORD ("not" would be wrong here, but "int" is an ident)
	typeTok := p.advance()
	if typeTok.Type != TOKEN_IDENT && typeTok.Type != TOKEN_KEYWORD {
		return nil, fmt.Errorf("expected type name, got %q", typeTok.Value)
	}
	typeName := typeTok.Value

	col := &ast.ColumnSpec{
		Name:     nameTok.Value,
		TypeName: typeName,
	}

	// optional constraints: NOT NULL, PRIMARY KEY (in any order)
	for {
		if p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "NOT" {
			p.advance()
			if err := p.expectKeyword("NULL"); err != nil {
				return nil, err
			}
			col.NotNull = true
		} else if p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "PRIMARY" {
			p.advance()
			if err := p.expectKeyword("KEY"); err != nil {
				return nil, err
			}
			col.PrimaryKey = true
			col.NotNull = true // PRIMARY KEY implies NOT NULL
		} else {
			break
		}
	}

	return col, nil
}

func (p *Parser) parseDropTable() (*ast.DropTableStatement, error) {
	// DROP
	if err := p.expectKeyword("DROP"); err != nil {
		return nil, err
	}
	// TABLE
	if err := p.expectKeyword("TABLE"); err != nil {
		return nil, err
	}
	// optional IF EXISTS
	ifExists := false
	if p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "IF" {
		p.advance()
		if err := p.expectKeyword("EXISTS"); err != nil {
			return nil, err
		}
		ifExists = true
	}
	// table name
	nameTok, err := p.expectToken(TOKEN_IDENT)
	if err != nil {
		return nil, err
	}
	// optional ;
	if p.peek().Type == TOKEN_SEMICOLON {
		p.advance()
	}

	return &ast.DropTableStatement{
		Name:     nameTok.Value,
		IfExists: ifExists,
	}, nil
}

func (p *Parser) parseInsert() (*ast.InsertStatement, error) {
	// INSERT
	if err := p.expectKeyword("INSERT"); err != nil {
		return nil, err
	}
	// INTO
	if err := p.expectKeyword("INTO"); err != nil {
		return nil, err
	}
	// table name
	nameTok, err := p.expectToken(TOKEN_IDENT)
	if err != nil {
		return nil, err
	}

	// optional column list
	var columns []string
	if p.peek().Type == TOKEN_LPAREN {
		// could be column list or VALUES — peek ahead
		// if next after LPAREN is IDENT, it's a column list
		// VALUES always starts with keyword VALUES before LPAREN
		p.advance() // consume (
		for {
			col, err := p.expectToken(TOKEN_IDENT)
			if err != nil {
				return nil, err
			}
			columns = append(columns, col.Value)

			if p.peek().Type == TOKEN_COMMA {
				p.advance()
				continue
			}
			break
		}
		if _, err := p.expectToken(TOKEN_RPAREN); err != nil {
			return nil, err
		}
	}
	// VALUES
	if err := p.expectKeyword("VALUES"); err != nil {
		return nil, err
	}
	// (
	if _, err := p.expectToken(TOKEN_LPAREN); err != nil {
		return nil, err
	}
	// expression list
	var values []ast.Expression
	for {
		expr, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		values = append(values, expr)

		if p.peek().Type == TOKEN_COMMA {
			p.advance()
			continue
		}
		break
	}
	// )
	if _, err := p.expectToken(TOKEN_RPAREN); err != nil {
		return nil, err
	}

	// optional ;
	if p.peek().Type == TOKEN_SEMICOLON {
		p.advance()
	}

	return &ast.InsertStatement{
		Table:   nameTok.Value,
		Columns: columns,
		Values:  values,
	}, nil
}

func (p *Parser) parseSelect() (*ast.SelectStatement, error) {
	// SELECT
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}

	// column list
	var columns []string
	if p.peek().Type == TOKEN_ASTERISK {
		p.advance()
		columns = []string{"*"}
	} else {
		for {
			col, err := p.expectToken(TOKEN_IDENT)
			if err != nil {
				return nil, err
			}
			columns = append(columns, col.Value)

			if p.peek().Type == TOKEN_COMMA {
				p.advance()
				continue
			}
			break
		}
	}

	// FROM
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}

	// table name
	nameTok, err := p.expectToken(TOKEN_IDENT)
	if err != nil {
		return nil, err
	}

	// optional WHERE
	var where ast.Expression
	if p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "WHERE" {
		p.advance()
		where, err = p.parseWhereExpr()
		if err != nil {
			return nil, err
		}
	}

	// optional ;
	if p.peek().Type == TOKEN_SEMICOLON {
		p.advance()
	}

	return &ast.SelectStatement{
		Columns: columns,
		Table:   nameTok.Value,
		Where:   where,
	}, nil
}

func (p *Parser) parsePrimary() (ast.Expression, error) {
	tok := p.peek()

	switch tok.Type {
	case TOKEN_NUMBER:
		p.advance()
		if strings.Contains(tok.Value, ".") {
			f, err := strconv.ParseFloat(tok.Value, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid float: %q", tok.Value)
			}
			return &ast.FloatLiteral{Value: f}, nil
		}
		i, err := strconv.ParseInt(tok.Value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer: %q", tok.Value)
		}
		return &ast.IntegerLiteral{Value: i}, nil

	case TOKEN_STRING:
		p.advance()
		return &ast.StringLiteral{Value: tok.Value}, nil

	case TOKEN_IDENT:
		p.advance()
		return &ast.ColumnRef{Name: tok.Value}, nil

	case TOKEN_LPAREN:
		p.advance()
		expr, err := p.parseWhereExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expectToken(TOKEN_RPAREN); err != nil {
			return nil, err
		}
		return expr, nil

	case TOKEN_KEYWORD:
		switch tok.Value {
		case "TRUE":
			p.advance()
			return &ast.BoolLiteral{Value: true}, nil
		case "FALSE":
			p.advance()
			return &ast.BoolLiteral{Value: false}, nil
		case "NULL":
			p.advance()
			return &ast.NullLiteral{}, nil
		}
	}

	return nil, fmt.Errorf("expected expression, got %q", tok.Value)
}

func (p *Parser) parseWhereExpr() (ast.Expression, error) {
	left, err := p.parseAndExpr()
	if err != nil {
		return nil, err
	}

	for p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "OR" {
		p.advance()
		right, err := p.parseAndExpr()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: "OR", Right: right}
	}

	return left, nil
}

func (p *Parser) parseAndExpr() (ast.Expression, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}

	for p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "AND" {
		p.advance()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: "AND", Right: right}
	}

	return left, nil
}

func (p *Parser) parseComparison() (ast.Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	// check for IS [NOT] NULL
	if p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "IS" {
		p.advance()
		not := false
		if p.peek().Type == TOKEN_KEYWORD && p.peek().Value == "NOT" {
			p.advance()
			not = true
		}
		if err := p.expectKeyword("NULL"); err != nil {
			return nil, err
		}
		return &ast.IsNullExpr{Expr: left, Not: not}, nil
	}

	// check for comparison operators
	switch p.peek().Type {
	case TOKEN_EQ, TOKEN_NEQ, TOKEN_LT, TOKEN_GT, TOKEN_LTE, TOKEN_GTE:
		op := p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &ast.BinaryExpr{Left: left, Op: op.Value, Right: right},
			nil
	}

	return left, nil
}

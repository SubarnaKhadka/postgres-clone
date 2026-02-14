package parser

import "strings"

type TokenType int

const (
	TOKEN_KEYWORD TokenType = iota
	TOKEN_IDENT
	TOKEN_LPAREN
	TOKEN_RPAREN
	TOKEN_COMMA
	TOKEN_SEMICOLON
	TOKEN_STRING
	TOKEN_NUMBER
	TOKEN_ASTERISK
	TOKEN_EQ
	TOKEN_NEQ
	TOKEN_LT
	TOKEN_GT
	TOKEN_LTE
	TOKEN_GTE
	TOKEN_EOF
)

type Token struct {
	Type  TokenType
	Value string
}

var keywords = map[string]bool{
	"CREATE":  true,
	"TABLE":   true,
	"DROP":    true,
	"IF":      true,
	"EXISTS":  true,
	"NOT":     true,
	"NULL":    true,
	"PRIMARY": true,
	"KEY":     true,
	"INSERT":  true,
	"INTO":    true,
	"VALUES":  true,
	"TRUE":    true,
	"FALSE":   true,
	"SELECT":  true,
	"FROM":    true,
	"WHERE":   true,
	"AND":     true,
	"OR":      true,
	"IS":      true,
}

type Lexer struct {
	input string
	pos   int
}

func NewLexer(input string) *Lexer {
	return &Lexer{input: input}
}

func (l *Lexer) NextToken() Token {
	// skip whitespace
	for l.pos < len(l.input) && (l.input[l.pos] == ' ' || l.input[l.pos] == '\t' || l.input[l.pos] == '\n' || l.input[l.pos] == '\r') {
		l.pos++
	}
	// check EOF
	if l.pos >= len(l.input) {
		return Token{TOKEN_EOF, ""}
	}
	// single-character tokens
	switch l.input[l.pos] {
	case '(':
		l.pos++
		return Token{TOKEN_LPAREN, "("}
	case ')':
		l.pos++
		return Token{TOKEN_RPAREN, ")"}
	case ',':
		l.pos++
		return Token{TOKEN_COMMA, ","}
	case ';':
		l.pos++
		return Token{TOKEN_SEMICOLON, ";"}
	case '*':
		l.pos++
		return Token{TOKEN_ASTERISK, "*"}
	case '=':
		l.pos++
		return Token{TOKEN_EQ, "="}
	case '!':
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '=' {
			l.pos++
			return Token{TOKEN_NEQ, "!="}
		}
		return Token{TOKEN_IDENT, "!"} // '!' — invalid SQL, but don't corrupt state
	case '<':
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '=' {
			l.pos++
			return Token{TOKEN_LTE, "<="}
		}
		if l.pos < len(l.input) && l.input[l.pos] == '>' {
			l.pos++
			return Token{TOKEN_NEQ, "<>"}
		}
		return Token{TOKEN_LT, "<"}
	case '>':
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '=' {
			l.pos++
			return Token{TOKEN_GTE, ">="}
		}
		return Token{TOKEN_GT, ">"}
	}

	// string literal
	if l.input[l.pos] == '\'' {
		l.pos++ // skip opening quote
		start := l.pos
		for l.pos < len(l.input) && l.input[l.pos] != '\'' {
			l.pos++
		}
		value := l.input[start:l.pos]
		if l.pos < len(l.input) {
			l.pos++ // skip closing quote
		}
		return Token{TOKEN_STRING, value}
	}

	// number literal
	if isDigit(l.input[l.pos]) {
		start := l.pos
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			l.pos++
		}
		// check for decimal point
		if l.pos < len(l.input) && l.input[l.pos] == '.' {
			l.pos++
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				l.pos++
			}
		}
		return Token{TOKEN_NUMBER, l.input[start:l.pos]}
	}

	// read a word
	if isLetter(l.input[l.pos]) || l.input[l.pos] == '_' {
		start := l.pos
		for l.pos < len(l.input) && (isLetter(l.input[l.pos]) ||
			isDigit(l.input[l.pos]) || l.input[l.pos] == '_') {
			l.pos++
		}
		word := l.input[start:l.pos]

		// We have a word, but is it a SQL keyword or a user-defined name?
		// keyword or identifier
		upper := strings.ToUpper(word)
		if keywords[upper] {
			return Token{TOKEN_KEYWORD, upper}
		}
		return Token{TOKEN_IDENT, word}
	}

	// unknown character
	ch := string(l.input[l.pos])
	l.pos++
	return Token{TOKEN_IDENT, ch}
}

func isLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

package src

import (
	"fmt"
	"io"
	"strings"
)

type Buffer struct {
	tok Token
	lit string
	n   int
}

type Parser struct {
	s   *Scanner
	buf Buffer
}

type SetStatement struct {
	Shell []string
	Vars  map[string]string
}

func NewParser(r io.Reader) *Parser {
	return &Parser{s: NewScanner(r)}
}

func (p *Parser) scan() (tok Token, lit string) {
	if p.buf.n != 0 {
		p.buf.n = 0
		return p.buf.tok, p.buf.lit
	}

	tok, lit = p.s.Scan()
	p.buf.tok, p.buf.lit = tok, lit

	return p.buf.tok, p.buf.lit
}

func (p *Parser) unscan() { p.buf.n = 1 }

func (p *Parser) scanIgnoreWhitespace() (tok Token, lit string) {
	tok, lit = p.scan()
	if tok == WS {
		tok, lit = p.scan()
	}
	return
}

func (p *Parser) Parse() (*SetStatement, error) {
	stmt := &SetStatement{}

	if tok, lit := p.scanIgnoreWhitespace(); tok != SET {
		return nil, fmt.Errorf("found %q, expected SET", lit)
	}

	tok, lit := p.scanIgnoreWhitespace()
	if tok != IDENT && tok != SHELL {
		return nil, fmt.Errorf("found %q, expected variable name or shell specs", lit)
	} else if strings.HasPrefix(lit, "[") {
		for {
			tok, lit := p.scanIgnoreWhitespace()
			if tok != IDENT && tok != ASTERISK {
				return nil, fmt.Errorf("found %q, expected field", lit)
			}
			stmt.Shell = append(stmt.Shell, lit)
			fmt.Println(lit)

			// If the next token is not a comma then break the loop.
			if tok, _ := p.scanIgnoreWhitespace(); tok != COMMA {
				p.unscan()
				break
			}
		}
	}

	if tok, lit := p.scanIgnoreWhitespace(); tok != EQUALS_TO {
		return nil, fmt.Errorf("found %q, expected '='", lit)
	}

	return stmt, nil
}

package src

import (
	"fmt"

	participle "github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

type Chasefile struct {
	Pos lexer.Position

	Entries []*Entry `parser:"@@*"`
}

type Entry struct {
	Section *Section `parser:"	@@"`
	Assign  *Assign  `parser:"| @@"`
}

type Assign struct {
	Key   string `parser:"'set' (@Ident | 'shell') '='"`
	Value *Value `parser:"| @@"`
}

type Step struct {
	Key   string `parser:"(@'summary' | 'uses' | 'usage' | 'cmds') ':'"`
	Value *Value `parser:"| @@"`
}

type Value struct {
	String *string  `parser:"	@QuotedString | @UnquotedString | @Ident"`
	Number *float64 `parser:"| @Number"`
	Bool   *bool    `parser:"| (@'true' | 'false'| 'TRUE' | 'FALSE'| 'True' | 'False')"`
	List   []*Value `parser:"| '[' ( @@ ( ',' @@ )* )? ']'"`
}

type Section struct {
	Name  string  `parser:"@(Ident ( '.' Ident )*) ':'"`
	Steps []*Step `parser:"@@"`
}

var (
	chasefileLexer = lexer.MustSimple([]lexer.SimpleRule{
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z_0-9]*`},
		{Name: "UnquotedString", Pattern: `(?:[a-zA-Z0-9_\-\.\#\{\}\>\%]+)`},
		{Name: "QuotedString", Pattern: `(?:\"(?:[^\"]|\\.)*\")`},
		{Name: "Number", Pattern: `[-+]?[.0-9]+\b`},
		{Name: "Punct", Pattern: `\[|]|[-!()+/*=,:]`},
		{Name: "whitespace", Pattern: `\s+`},
	})

	ChasefileParser = participle.MustBuild[Chasefile](
		participle.Lexer(chasefileLexer),
		participle.Unquote("QuotedString"),
	)
)

func ParseQuery(c string) (*Chasefile, error) {
	// err := parser.ParseFile
	expr, err := ChasefileParser.ParseString("", c)
	if err != nil {
		return nil, fmt.Errorf("chase: error opening chasefile: %w", err)
	}

	return expr, nil
}

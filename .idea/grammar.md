### chasefile grammar
```
program       ::= statement*

statement     ::= "set" shell ":" "[" value ("," value)* "]"  
               | "set" variable "=" value
               | identifier ":" block

block         ::= "{" statement* "}"

value         ::= ident | unquotedString | quotedString | number | list | array | identifier

list          ::= "[" value ("," value)* "]"

array         ::= "[" block ("," block)* "]"

ident         ::= /[a-zA-Z_][a-zA-Z_0-9]*/

unquotedString ::= /(?:[a-zA-Z0-9_\-\.\#\{\}\>\%]+)/

quotedString  ::= /\"(?:[^\"]|\\.)*\"/

number        ::= /[-+]?[.0-9]+\b/

identifier    ::= letter (letter | digit | "_")*

variable      ::= uppercase_letter (letter | digit | "_")*

letter        ::= "a" .. "z" | "A" .. "Z"

digit         ::= "0" .. "9"

uppercase_letter ::= "A" .. "Z"
```

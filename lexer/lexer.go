// Package lexer tokenizes qyvora-script source.
//
// It is generalized from TOHA3EE's original internal/script/lexer.go: the
// token set and grammar are unchanged, only the identifiers are exported so
// the parser can consume them. Newlines are significant: every statement lives
// on one line and blocks are closed with `end`.
package lexer

import (
	"fmt"
	"strings"
)

// Kind identifies a single lexical token.
type Kind int

// Token kinds. The iota block is an ordered enum; new kinds must be appended,
// never inserted, to keep any persisted or test expectations stable.
const (
	EOF Kind = iota
	Newline
	Ident  // names, module ids, keywords, raw values
	Str    // string literal
	Arrow  // ->
	Assign // =
	Append // >>
	Eq     // ==
	Ne     // !=
	Lt     // <
	Gt     // >
	Le     // <=
	Ge     // >=
	And    // &&
	Or     // ||
	Not    // !
	LBrack // [
	RBrack // ]
	Comma  // ,
	LParen // (
	RParen // )
	Dollar // $(
)

// Token is a single lexical token with its source position.
type Token struct {
	Kind Kind
	Text string
	Line int
	Raw  bool // true: single-quoted literal, no interpolation
}

func (t Token) String() string {
	switch t.Kind {
	case EOF:
		return "end of script"
	case Newline:
		return "newline"
	default:
		return fmt.Sprintf("%q", t.Text)
	}
}

// Lexer turns source text into a stream of tokens.
type Lexer struct {
	src  string
	pos  int // current byte offset into src
	line int // 1-based line of the current position
	toks []Token
}

// Lex tokenizes src. It returns the full token slice (including a final EOF).
func Lex(src string) ([]Token, error) {
	l := &Lexer{src: src, line: 1}
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		l.toks = append(l.toks, tok)
		if tok.Kind == EOF {
			break
		}
	}
	return l.toks, nil
}

// next produces the next token.
func (l *Lexer) next() (Token, error) {
	// The loop skips whitespace and comments before returning the next real
	// token; the explicit returns below exit as soon as one is found.
	for l.pos < len(l.src) {
		c := l.src[l.pos]

		switch {
		case c == '\n':
			// Newlines are real tokens: they terminate statements, so the
			// line number is captured before it increments.
			l.pos++
			line := l.line
			l.line++
			return Token{Kind: Newline, Line: line}, nil

		case c == ' ' || c == '\t' || c == '\r':
			// Horizontal whitespace is insignificant between tokens.
			l.pos++
			continue

		case c == '#': // comment to end of line
			l.skipComment()
			continue

		case c == '/':
			// '//' is a comment; a lone '/' falls through to lexRaw (e.g. a
			// filesystem path or CIDR-adjacent text).
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
				l.skipComment()
				continue
			}
			// Bare '/' is an arithmetic operator.
			l.pos++
			return Token{Kind: Ident, Text: "/", Line: l.line}, nil

		case c == '-':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
				tok := Token{Kind: Arrow, Text: "->", Line: l.line}
				l.pos += 2
				return tok, nil
			}
			// A bare '-' (not '->') is part of a raw value: negative numbers
			// like "-1" and command-line flags like "exec nmap -sS".
			return l.lexRaw()

		case c == '>':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
				tok := Token{Kind: Append, Text: ">>", Line: l.line}
				l.pos += 2
				return tok, nil
			}
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
				tok := Token{Kind: Ge, Text: ">=", Line: l.line}
				l.pos += 2
				return tok, nil
			}
			l.pos++
			return Token{Kind: Gt, Text: ">", Line: l.line}, nil

		case c == '<':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
				tok := Token{Kind: Le, Text: "<=", Line: l.line}
				l.pos += 2
				return tok, nil
			}
			l.pos++
			return Token{Kind: Lt, Text: "<", Line: l.line}, nil

		case c == '=':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
				tok := Token{Kind: Eq, Text: "==", Line: l.line}
				l.pos += 2
				return tok, nil
			}
			l.pos++
			return Token{Kind: Assign, Text: "=", Line: l.line}, nil

		case c == '!':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
				tok := Token{Kind: Ne, Text: "!=", Line: l.line}
				l.pos += 2
				return tok, nil
			}
			l.pos++
			return Token{Kind: Not, Text: "!", Line: l.line}, nil

		case c == '&':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '&' {
				tok := Token{Kind: And, Text: "&&", Line: l.line}
				l.pos += 2
				return tok, nil
			}
			// A lone '&' is not valid in scripts; it falls through to the
			// unexpected-character error below.

		case c == '|':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '|' {
				tok := Token{Kind: Or, Text: "||", Line: l.line}
				l.pos += 2
				return tok, nil
			}

		case c == '[':
			l.pos++
			return Token{Kind: LBrack, Line: l.line}, nil

		case c == ']':
			l.pos++
			return Token{Kind: RBrack, Line: l.line}, nil

		case c == ',':
			l.pos++
			return Token{Kind: Comma, Line: l.line}, nil

		case c == '(':
			l.pos++
			return Token{Kind: LParen, Line: l.line}, nil

		case c == ')':
			l.pos++
			return Token{Kind: RParen, Line: l.line}, nil

		case c == '$':
			// '$(' starts an interpolation; a bare '$' is a lex error.
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '(' {
				tok := Token{Kind: Dollar, Text: "$(", Line: l.line}
				l.pos += 2
				return tok, nil
			}

		case c == '+':
			// Bare '+' is an arithmetic operator.
			l.pos++
			return Token{Kind: Ident, Text: "+", Line: l.line}, nil

		case c == '*':
			// Bare '*' is an arithmetic operator.
			l.pos++
			return Token{Kind: Ident, Text: "*", Line: l.line}, nil

		case c == '"':
			return l.lexQuoted()

		case c == '\'':
			return l.lexSingleQuoted()

		case isIdentStart(c):
			return l.lexIdent()

		default:
			// Anything else (e.g. '~') must begin a raw value or be an
			// error; lexRaw decides which.
			return l.lexRaw()
		}

		return Token{}, fmt.Errorf("line %d: unexpected character %q", l.line, string(c))
	}
	return Token{Kind: EOF, Line: l.line}, nil
}

func (l *Lexer) skipComment() {
	// Consume up to but not including the newline, which the main loop will
	// emit as its own token.
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

// lexQuoted reads a double-quoted string with backslash escapes and $(...)
// interpolation. The raw text (without quotes) is preserved so the parser can
// split it into literal and interpolation segments.
func (l *Lexer) lexQuoted() (Token, error) {
	line := l.line
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case '"':
			l.pos++
			return Token{Kind: Str, Text: b.String(), Line: line}, nil
		case '\n':
			// Strings are single-line by design; a newline means the user
			// forgot the closing quote.
			return Token{}, fmt.Errorf("line %d: unterminated string", line)
		case '\\':
			if l.pos+1 >= len(l.src) {
				return Token{}, fmt.Errorf("line %d: trailing backslash in string", line)
			}
			esc := l.src[l.pos+1]
			switch esc {
			// Recognized escapes map to their control/quote equivalents; any
			// unknown escape (e.g. "\d") is kept verbatim so Windows paths and
			// regexes survive unchanged.
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case '$':
				b.WriteByte('$')
			default:
				b.WriteByte('\\')
				b.WriteByte(esc)
			}
			l.pos += 2
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	return Token{}, fmt.Errorf("line %d: unterminated string", line)
}

// lexSingleQuoted reads a single-quoted literal string (no escapes, no
// interpolation). It is the escape hatch when a value contains "$(...)" or
// backslashes that must stay literal.
func (l *Lexer) lexSingleQuoted() (Token, error) {
	line := l.line
	l.pos++
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case '\'':
			l.pos++
			return Token{Kind: Str, Text: b.String(), Line: line, Raw: true}, nil
		case '\n':
			return Token{}, fmt.Errorf("line %d: unterminated string", line)
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	return Token{}, fmt.Errorf("line %d: unterminated string", line)
}

// lexIdent reads an identifier: starts with a letter or underscore, then
// letters, digits, underscores and dots (so module keys like "arp.spoof" and
// variables like "_hosts" both lex as one token).
func (l *Lexer) lexIdent() (Token, error) {
	line := l.line
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
		l.pos++
	}
	return Token{Kind: Ident, Text: l.src[start:l.pos], Line: line}, nil
}

// lexRaw reads a bare, unquoted value such as a number, an IP, a duration or
// a CIDR ("30", "5ms", "10.0.0.5", "192.168.1.0/24"). Values are carried as
// strings and parsed on demand. An unexpected character that cannot start a
// raw value is a lex error rather than being silently dropped.
func (l *Lexer) lexRaw() (Token, error) {
	line := l.line
	start := l.pos
	for l.pos < len(l.src) && isRawPart(l.src[l.pos]) {
		l.pos++
	}
	if l.pos == start {
		// No consumable characters: the character that led us here cannot
		// begin any valid token, so report it explicitly.
		return Token{}, fmt.Errorf("line %d: unexpected character %q", line, string(l.src[l.pos]))
	}
	// Raw values lex as Str so the parser treats them like literals.
	return Token{Kind: Str, Text: l.src[start:l.pos], Line: line}, nil
}

// isIdentStart reports whether c can begin an identifier: underscore or letter.
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart reports whether c may continue an identifier, including the '.'
// that separates dotted module keys ("arp.spoof").
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.'
}

// isRawPart matches characters that make up unquoted literal values. Leading
// digits (numbers, IPs, durations, CIDRs) all funnel through here.
func isRawPart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '.' || c == '/' || c == ':' ||
		c == '-' || c == '_' || c == '+'
}

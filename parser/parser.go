// Package parser is a recursive-descent parser for the shared qyvora-script
// language. It is generalized from TOHA3EE's original internal/script/parser.go
// and produces ast nodes. Newlines terminate statements; blocks are opened with
// if/for/repeat/while and closed with `end` (or `else` for the middle of an if).
package parser

import (
	"fmt"
	"strings"
	"time"

	"github.com/QYVORA/qyvora-script/ast"
	"github.com/QYVORA/qyvora-script/lexer"
)

// parser is a recursive-descent parser.
type parser struct {
	toks []lexer.Token // the full token stream, ending in EOF
	pos  int           // index of the next unconsumed token
}

// Parse parses src into a Program.
func Parse(src string) (*ast.Program, error) {
	toks, err := lexer.Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	return p.parseProgram()
}

// parseError reports a syntax error at a line.
func parseError(tok lexer.Token, format string, args ...any) error {
	// Format is expanded eagerly so the caller's variadic args are consumed
	// exactly once even when wrapped by fmt.Errorf later.
	return fmt.Errorf("script: line %d: %s", tok.Line, fmt.Sprintf(format, args...))
}

// peek returns the next token without consuming it.
func (p *parser) peek() lexer.Token { return p.toks[p.pos] }

// at reports whether the next token has the given kind.
func (p *parser) at(kind lexer.Kind) bool { return p.peek().Kind == kind }

// next consumes and returns the next token. It never advances past the final
// EOF token, so a well-formed stream always has a token to return.
func (p *parser) next() lexer.Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

// expect consumes the next token, failing with a parseError if its kind does
// not match. what is the human-readable description shown in the error.
func (p *parser) expect(kind lexer.Kind, what string) (lexer.Token, error) {
	t := p.peek()
	if t.Kind != kind {
		return t, parseError(t, "expected %s, found %s", what, t)
	}
	return p.next(), nil
}

// skipNewlines consumes newline tokens (and stops at non-newline tokens).
func (p *parser) skipNewlines() {
	for p.at(lexer.Newline) {
		p.next()
	}
}

// parseProgram parses a whole script.
func (p *parser) parseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	p.skipNewlines()
	// Parse statements until the stream is exhausted; a trailing newline
	// before EOF is fine.
	for !p.at(lexer.EOF) {
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		prog.Stmts = append(prog.Stmts, stmt)
		p.skipNewlines()
	}
	return prog, nil
}

// parseStmt parses a single statement (already positioned at its first token).
func (p *parser) parseStmt() (ast.Stmt, error) {
	t := p.peek()
	if t.Kind == lexer.Newline || t.Kind == lexer.EOF {
		return nil, parseError(t, "expected a statement, found %s", t)
	}

	// Keywords are recognized only when they appear as a plain identifier.
	if t.Kind == lexer.Ident {
		switch t.Text {
		case "set":
			return p.parseSet()
		case "get":
			return p.parseGet()
		case "on", "start", "run":
			return p.parseStart()
		case "off":
			return p.parseStop()
		case "stop":
			// `stop` alone halts the script; `stop <module>` stops a module.
			// Disambiguate by peeking whether another token follows on the
			// same line.
			if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind != lexer.Newline && p.toks[p.pos+1].Kind != lexer.EOF {
				return p.parseStop()
			}
			p.next()
			return ast.HaltStmt{}, nil
		case "wait":
			return p.parseWait()
		case "sleep":
			return p.parseSleep()
		case "echo", "say", "print":
			return p.parseEcho()
		case "show":
			return p.parseShow()
		case "exec", "run.cmd", "command":
			return p.parseExec()
		case "report":
			return p.parseReport()
		case "if":
			return p.parseIf()
		case "for":
			return p.parseForEach()
		case "repeat":
			return p.parseRepeat()
		case "while":
			return p.parseWhile()
		case "try":
			return p.parseTry()
		case "def":
			return p.parseFuncDef()
		case "break":
			p.next()
			return ast.BreakStmt{}, nil
		case "continue":
			p.next()
			return ast.ContinueStmt{}, nil
		case "end", "else":
			// These block terminators are only valid inside a block; seeing
			// one here means the source structure is wrong.
			return nil, parseError(t, "unexpected %q", t.Text)
		}

		// Variable assignment: `_var -> val` / `_var = val` / `_var >> val`.
		if strings.HasPrefix(t.Text, "_") {
			op := p.toks[p.pos+1].Kind
			if op == lexer.Arrow || op == lexer.Assign || op == lexer.Append {
				p.next() // var
				opTok := p.next()
				val, err := p.parseExprValue()
				if err != nil {
					return nil, err
				}
				// Normalize the two assign operators to a canonical spelling
				// so the engine only has to handle "=" and ">>".
				op := "="
				switch opTok.Kind {
				case lexer.Arrow:
					op = "->"
				case lexer.Append:
					op = ">>"
				}
				return ast.AssignStmt{Var: t.Text, Op: op, Value: val}, nil
			}
		}

		// Function call: `funcname arg1 arg2` or `funcname` (no args)
		// Must be an identifier that is NOT a keyword and NOT underscore-prefixed.
		// Only valid at the start of a line (previous token was newline or EOF).
		// Not valid when followed by -> or = (those are assignments).
		if !strings.HasPrefix(t.Text, "_") && !isKeyword(t.Text) {
			// Check if we're at the start of a line
			if p.pos == 0 || p.toks[p.pos-1].Kind == lexer.Newline {
				// Check next token: if it's -> or =, this is not a function call
				if p.pos+1 < len(p.toks) {
					nextKind := p.toks[p.pos+1].Kind
					if nextKind == lexer.Arrow || nextKind == lexer.Assign || nextKind == lexer.Append {
						return nil, parseError(t, "expected a statement, found %s", t)
					}
					// Check if next token is on the same line (not newline/EOF)
					if nextKind != lexer.Newline && nextKind != lexer.EOF {
						return p.parseCall()
					}
				}
				// Bare function call with no args
				p.next()
				return ast.CallStmt{Name: t.Text}, nil
			}
		}
	}

	return nil, parseError(t, "expected a statement, found %s", t)
}

func (p *parser) parseSet() (ast.Stmt, error) {
	p.next() // set
	key, err := p.expect(lexer.Ident, "a module key like net.scan.targets")
	if err != nil {
		return nil, err
	}
	// Both "->" and "=" are accepted here for ergonomic flexibility.
	if !p.at(lexer.Arrow) && !p.at(lexer.Assign) {
		return nil, parseError(p.peek(), "expected '->' or '=', found %s", p.peek())
	}
	p.next()
	val, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	return ast.SetStmt{Key: key.Text, Value: val}, nil
}

func (p *parser) parseGet() (ast.Stmt, error) {
	p.next() // get
	key, err := p.expect(lexer.Ident, "a module key like net.scan.targets")
	if err != nil {
		return nil, err
	}
	if !p.at(lexer.Arrow) && !p.at(lexer.Assign) {
		return nil, parseError(p.peek(), "expected '->', found %s", p.peek())
	}
	p.next()
	v, err := p.expect(lexer.Ident, "a target variable like _targets")
	if err != nil {
		return nil, err
	}
	// Only underscore-prefixed names can be assigned to, keeping script
	// variables clearly distinct from keywords and module ids.
	if !strings.HasPrefix(v.Text, "_") {
		return nil, parseError(v, "target variable must start with '_'")
	}
	return ast.GetStmt{Key: key.Text, Var: v.Text}, nil
}

func (p *parser) parseStart() (ast.Stmt, error) {
	p.next() // on / start / run
	id, err := p.expect(lexer.Ident, "a module id like arp.spoof")
	if err != nil {
		return nil, err
	}
	// Inline "key value" options until end of line. Values may join
	// comma-separated pieces without quotes, so "ports 80,443" works.
	var opts []ast.Expr
	for !p.at(lexer.Newline) && !p.at(lexer.EOF) {
		if p.at(lexer.Comma) {
			// A comma between options is pure punctuation and is skipped.
			p.next()
			continue
		}
		var e ast.Expr
		var err error
		// Alternate between key and value: after an odd number of parsed
		// tokens we are expecting a value for the preceding key.
		if len(opts)%2 == 1 { // expecting a value
			e, err = p.parseOptValue()
		} else {
			e, err = p.parseExprValue()
		}
		if err != nil {
			return nil, err
		}
		opts = append(opts, e)
	}
	return ast.StartStmt{ID: id.Text, Opts: opts}, nil
}

// parseOptValue parses a module option value, joining any comma-separated
// pieces into a single string ("80,443", "10.0.0.1,10.0.0.2").
func (p *parser) parseOptValue() (ast.Expr, error) {
	first, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	if !p.at(lexer.Comma) {
		return first, nil
	}
	// There is at least one comma: merge all following pieces into one
	// StringLit with "," segments between them.
	segs := optValueSegs(first)
	for p.at(lexer.Comma) {
		p.next()
		e, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		segs = append(segs, ast.Seg{Text: ","})
		segs = append(segs, optValueSegs(e)...)
	}
	return ast.StringLit{Segs: segs}, nil
}

// optValueSegs flattens an option-value piece into interpolation segments so
// that joining comma-separated pieces keeps $(...) references live instead of
// turning them into literal text.
func optValueSegs(e ast.Expr) []ast.Seg {
	switch v := e.(type) {
	case ast.StringLit:
		return v.Segs
	case ast.PropExpr:
		return []ast.Seg{{Prop: v.Path}}
	case ast.IdentExpr:
		return []ast.Seg{{Var: v.Name}}
	default:
		return []ast.Seg{{Text: ast.ExprText(e)}}
	}
}

func (p *parser) parseStop() (ast.Stmt, error) {
	p.next() // off / stop
	id, err := p.expect(lexer.Ident, "a module id like arp.spoof")
	if err != nil {
		return nil, err
	}
	return ast.StopStmt{ID: id.Text}, nil
}

func (p *parser) parseWait() (ast.Stmt, error) {
	p.next() // wait
	// "wait for <module> [max <secs>]" waits on a module; anything else is
	// the bare "wait <secs>" sleep form.
	if p.at(lexer.Ident) && p.peek().Text == "for" {
		p.next() // for
		id, err := p.expect(lexer.Ident, "a module id like net.scan")
		if err != nil {
			return nil, err
		}
		// Default cap of ten minutes so an unlucky wait can never hang a
		// script forever.
		max := 10 * time.Minute
		if p.at(lexer.Ident) && p.peek().Text == "max" {
			p.next()
			valTok := p.peek()
			secs, err := p.parseExprValue()
			if err != nil {
				return nil, err
			}
			if n, ok := ast.NumOf(secs); ok {
				max = time.Duration(n * float64(time.Second))
			} else {
				return nil, parseError(valTok, "wait for %s max needs a number of seconds, found %s", id.Text, valTok)
			}
		}
		return ast.WaitStmt{Module: id.Text, Max: max}, nil
	}
	secs, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	return ast.SleepStmt{Seconds: secs}, nil
}

func (p *parser) parseSleep() (ast.Stmt, error) {
	p.next() // sleep
	// The "->" is optional, so both "sleep -> 2" and "sleep 2" parse.
	if p.at(lexer.Arrow) {
		p.next()
	}
	secs, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	return ast.SleepStmt{Seconds: secs}, nil
}

// parseExec captures the rest of the line verbatim as a one-shot command,
// so scripts can call commands like "net.profile" or "creds.show". Commas are
// re-inserted without surrounding spaces so "80,443" survives reconstruction.
func (p *parser) parseExec() (ast.Stmt, error) {
	p.next() // exec / run.cmd / command
	// Accept an optional arrow for the "exec -> <cmd>" idiom.
	if p.at(lexer.Arrow) || p.at(lexer.Append) {
		p.next()
	}
	var b strings.Builder
	needSpace := false
	for !p.at(lexer.Newline) && !p.at(lexer.EOF) {
		t := p.next()
		if t.Kind == lexer.Comma {
			// Reconstruct commas without spaces: the lexer strips them from
			// token text, and "80, 443" should come back as "80,443".
			b.WriteByte(',')
			needSpace = false
			continue
		}
		// Re-insert the single space that separated tokens in the source.
		if needSpace {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text)
		needSpace = true
	}
	return ast.ExecStmt{Raw: b.String()}, nil
}

func (p *parser) parseEcho() (ast.Stmt, error) {
	p.next() // echo / say / print
	// "->" is optional for symmetry with the other value-taking keywords.
	if p.at(lexer.Arrow) {
		p.next()
	}
	val, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	return ast.EchoStmt{Value: val}, nil
}

func (p *parser) parseShow() (ast.Stmt, error) {
	p.next() // show
	id, err := p.expect(lexer.Ident, "a module id like arp.spoof")
	if err != nil {
		return nil, err
	}
	return ast.ShowStmt{ID: id.Text}, nil
}

func (p *parser) parseReport() (ast.Stmt, error) {
	p.next() // report
	// Optional arrow so "report -> \"f.md\"" and "report \"f.md\"" both work.
	if p.at(lexer.Arrow) {
		p.next()
	}
	val, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	return ast.ReportStmt{Path: val}, nil
}

func (p *parser) parseIf() (ast.Stmt, error) {
	p.next() // if
	cond, err := p.parseCond()
	if err != nil {
		return nil, err
	}
	// The condition must end the line; anything after it is a syntax error.
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	then, term, err := p.parseBlock(true)
	if err != nil {
		return nil, err
	}
	stmt := ast.IfStmt{Cond: cond, Then: then}

	// Handle elif chains: if ... elif ... elif ... else ... end
	for term == "elif" {
		p.next() // elif
		elifCond, err := p.parseCond()
		if err != nil {
			return nil, err
		}
		if err := p.endOfLine(); err != nil {
			return nil, err
		}
		elifBody, term2, err := p.parseBlock(true)
		if err != nil {
			return nil, err
		}
		stmt.Elifs = append(stmt.Elifs, ast.ElifCond{Cond: elifCond, Body: elifBody})
		term = term2
	}

	// Final else branch
	if term == "else" {
		p.next() // else
		if err := p.endOfLine(); err != nil {
			return nil, err
		}
		els, term2, err := p.parseBlock(false)
		if err != nil {
			return nil, err
		}
		stmt.Else = els
		if term2 != "end" {
			return nil, parseError(p.peek(), "expected 'end', found %s", p.peek())
		}
		p.next()
		return stmt, nil
	}

	// No else: just close with end
	if term != "end" {
		return nil, parseError(p.peek(), "expected 'end', found %s", p.peek())
	}
	p.next()
	return stmt, nil
}

func (p *parser) parseForEach() (ast.Stmt, error) {
	p.next() // for
	if !p.at(lexer.Ident) || p.peek().Text != "each" {
		return nil, parseError(p.peek(), "expected 'each', found %s", p.peek())
	}
	p.next()
	v, err := p.expect(lexer.Ident, "a loop variable like _item")
	if err != nil {
		return nil, err
	}
	// Loop variables must be underscore-prefixed like regular variables.
	if !strings.HasPrefix(v.Text, "_") {
		return nil, parseError(v, "loop variable must start with '_'")
	}
	if !p.at(lexer.Ident) || p.peek().Text != "in" {
		return nil, parseError(p.peek(), "expected 'in', found %s", p.peek())
	}
	p.next()
	list, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	body, term, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	if term != "end" {
		return nil, parseError(p.peek(), "expected 'end', found %s", p.peek())
	}
	p.next()
	return ast.ForEachStmt{Var: v.Text, List: list, Body: body}, nil
}

func (p *parser) parseRepeat() (ast.Stmt, error) {
	p.next() // repeat
	n, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}
	// The "times" keyword is mandatory: "repeat 3 times ...".
	if !p.at(lexer.Ident) || p.peek().Text != "times" {
		return nil, parseError(p.peek(), "expected 'times', found %s", p.peek())
	}
	p.next()
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	body, term, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	if term != "end" {
		return nil, parseError(p.peek(), "expected 'end', found %s", p.peek())
	}
	p.next()
	return ast.RepeatStmt{N: n, Body: body}, nil
}

func (p *parser) parseWhile() (ast.Stmt, error) {
	p.next() // while
	cond, err := p.parseCond()
	if err != nil {
		return nil, err
	}
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	body, term, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	if term != "end" {
		return nil, parseError(p.peek(), "expected 'end', found %s", p.peek())
	}
	p.next()
	return ast.WhileStmt{Cond: cond, Body: body}, nil
}

// parseBlock parses statements until "end" (or "else"/"elif", when stopOnElse is
// true, or "catch" when stopOnCatch is true). It returns the statements plus
// the terminator token's text.
func (p *parser) parseBlock(stopOnElse bool, stopOnCatch ...bool) ([]ast.Stmt, string, error) {
	catchOk := len(stopOnCatch) > 0 && stopOnCatch[0]
	var stmts []ast.Stmt
	for {
		p.skipNewlines()
		t := p.peek()
		switch {
		case t.Kind == lexer.EOF:
			// Hitting EOF first means the block was never closed.
			return nil, "", parseError(t, "expected 'end' before end of script")
		case t.Kind == lexer.Ident && t.Text == "end":
			return stmts, "end", nil
		case t.Kind == lexer.Ident && (t.Text == "else" || t.Text == "elif") && stopOnElse:
			// Only the if-statement cares about 'else' and 'elif'; for loops
			// they would be plain (and likely invalid) statements.
			return stmts, t.Text, nil
		case t.Kind == lexer.Ident && t.Text == "catch" && catchOk:
			return stmts, "catch", nil
		case t.Kind == lexer.Newline:
			continue
		}
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, "", err
		}
		stmts = append(stmts, stmt)
	}
}

// endOfLine verifies the current statement line is finished (newline or EOF).
func (p *parser) endOfLine() error {
	t := p.peek()
	if t.Kind != lexer.Newline && t.Kind != lexer.EOF {
		return parseError(t, "unexpected %s after statement", t)
	}
	return nil
}

// -- conditions ------------------------------------------------------------

// parseCond parses a full condition: a chain of `||`-joined AND-terms.
// Precedence is intentionally flat: or (lowest) over and over not.
func (p *parser) parseCond() (ast.Cond, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.at(lexer.Or) {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = ast.OrCond{L: left, R: right}
	}
	return left, nil
}

// parseAnd parses a chain of `&&`-joined negated terms.
func (p *parser) parseAnd() (ast.Cond, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.at(lexer.And) {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = ast.AndCond{L: left, R: right}
	}
	return left, nil
}

// parseNot handles both the `!` operator and the English "not" keyword, and
// is recursive so "not not X" parses correctly.
func (p *parser) parseNot() (ast.Cond, error) {
	t := p.peek()
	if (t.Kind == lexer.Not) || (t.Kind == lexer.Ident && t.Text == "not") {
		p.next()
		c, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return ast.NotCond{C: c}, nil
	}
	return p.parsePrimaryCond()
}

func (p *parser) parsePrimaryCond() (ast.Cond, error) {
	// Bare true/false.
	if p.at(lexer.Ident) {
		if p.peek().Text == "true" || p.peek().Text == "false" {
			// A boolean is only a literal when nothing follows that could make
			// it a comparison operand, e.g. "true == false".
			next := p.toks[p.pos+1]
			if !isCondOp(next) {
				p.next()
				return ast.BoolCond{Value: p.toks[p.pos-1].Text == "true"}, nil
			}
		}
	}

	left, err := p.parseExprValue()
	if err != nil {
		return nil, err
	}

	t := p.peek()
	switch {
	case t.Kind == lexer.Eq:
		p.next()
		right, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		return ast.CmpCond{Op: "==", L: left, R: right}, nil
	case t.Kind == lexer.Ne:
		p.next()
		right, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		return ast.CmpCond{Op: "!=", L: left, R: right}, nil
	case t.Kind == lexer.Lt:
		p.next()
		right, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		return ast.CmpCond{Op: "<", L: left, R: right}, nil
	case t.Kind == lexer.Gt:
		p.next()
		right, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		return ast.CmpCond{Op: ">", L: left, R: right}, nil
	case t.Kind == lexer.Le:
		p.next()
		right, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		return ast.CmpCond{Op: "<=", L: left, R: right}, nil
	case t.Kind == lexer.Ge:
		p.next()
		right, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		return ast.CmpCond{Op: ">=", L: left, R: right}, nil
	case t.Kind == lexer.Ident && t.Text == "contains":
		p.next()
		right, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		return ast.ContainsCond{L: left, R: right}, nil
	case t.Kind == lexer.Ident && t.Text == "is":
		p.next()
		// "X is not running" is a negated running check.
		neg := false
		if p.at(lexer.Ident) && p.peek().Text == "not" {
			neg = true
			p.next()
		}
		if !p.at(lexer.Ident) || p.peek().Text != "running" {
			return nil, parseError(p.peek(), "expected 'running', found %s", p.peek())
		}
		p.next()
		return ast.RunningCond{Module: left, Negate: neg}, nil
	default:
		// A single bare "true"/"false" that lexed as an identifier (rather
		// than through the Ident branch above) still becomes a BoolCond.
		if sl, ok := left.(ast.StringLit); ok && len(sl.Segs) == 1 {
			seg := sl.Segs[0]
			if seg.Text == "true" {
				return ast.BoolCond{Value: true}, nil
			}
			if seg.Text == "false" {
				return ast.BoolCond{Value: false}, nil
			}
		}
		return nil, parseError(t, "expected a comparison, found %s", t)
	}
}

// isCondOp reports whether tok can continue a condition expression.
func isCondOp(t lexer.Token) bool {
	switch t.Kind {
	case lexer.Eq, lexer.Ne, lexer.Lt, lexer.Gt, lexer.Le, lexer.Ge, lexer.And, lexer.Or:
		return true
	}
	return t.Kind == lexer.Ident && (t.Text == "contains" || t.Text == "is")
}

// -- expressions -----------------------------------------------------------

// isArithOp reports whether the current token is an arithmetic operator.
func (p *parser) isArithOp() bool {
	t := p.peek()
	if t.Kind == lexer.Ident {
		return t.Text == "+" || t.Text == "-" || t.Text == "*" || t.Text == "/"
	}
	return false
}

// arithOpText returns the operator text from the current token.
func (p *parser) arithOpText() string {
	return p.peek().Text
}

// parseArithExpr parses arithmetic expressions with + and - (lowest precedence).
func (p *parser) parseArithExpr() (ast.Expr, error) {
	left, err := p.parseArithTerm()
	if err != nil {
		return nil, err
	}
	for p.isArithOp() {
		op := p.arithOpText()
		if op == "+" || op == "-" {
			p.next()
			right, err := p.parseArithTerm()
			if err != nil {
				return nil, err
			}
			left = ast.ArithExpr{Op: op, Left: left, Right: right}
		} else {
			break
		}
	}
	return left, nil
}

// parseArithTerm parses arithmetic expressions with * and / (higher precedence).
func (p *parser) parseArithTerm() (ast.Expr, error) {
	left, err := p.parseExprAtom()
	if err != nil {
		return nil, err
	}
	for p.isArithOp() {
		op := p.arithOpText()
		if op == "*" || op == "/" {
			p.next()
			right, err := p.parseExprAtom()
			if err != nil {
				return nil, err
			}
			left = ast.ArithExpr{Op: op, Left: left, Right: right}
		} else {
			break
		}
	}
	return left, nil
}

// parseExprValue parses a value expression: possibly with arithmetic operators.
func (p *parser) parseExprValue() (ast.Expr, error) {
	return p.parseArithExpr()
}

// parseExprAtom parses a single atomic value expression: string, number, variable,
// $(...) property, or list.
func (p *parser) parseExprAtom() (ast.Expr, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.Str:
		p.next()
		if t.Raw {
			// Single-quoted strings are literal: no escapes, no $(...) interpolation.
			return ast.StringLit{Segs: []ast.Seg{{Text: t.Text}}}, nil
		}
		return parseSegments(t.Text), nil
	case lexer.Ident:
		p.next()
		// Underscore-prefixed tokens are variable references; any other bare
		// word is literal text (so "on arp.spoof" passes "arp.spoof" around
		// as a string, not an identifier to resolve).
		if strings.HasPrefix(t.Text, "_") {
			return ast.IdentExpr{Name: t.Text}, nil
		}
		return ast.StringLit{Segs: []ast.Seg{{Text: t.Text}}}, nil
	case lexer.Dollar:
		// "$(path)" reads a live session property; the path is a single ident
		// token terminated by ')'.
		p.next()
		path, err := p.expect(lexer.Ident, "a property path like hosts.count")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RParen, "')'"); err != nil {
			return nil, err
		}
		return ast.PropExpr{Path: path.Text}, nil
	case lexer.LBrack:
		return p.parseList()
	case lexer.LParen:
		// Parentheses group a value expression; the group is transparent and
		// the inner expression is returned directly.
		p.next()
		inner, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RParen, "')'"); err != nil {
			return nil, err
		}
		return inner, nil
	default:
		return nil, parseError(t, "expected a value, found %s", t)
	}
}

func (p *parser) parseList() (ast.Expr, error) {
	p.next() // [
	list := ast.ListExpr{}
	p.skipInline()
	for !p.at(lexer.RBrack) {
		item, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		list.Items = append(list.Items, item)
		p.skipInline()
		// Commas between items are optional, so "[a b c]" parses too.
		if p.at(lexer.Comma) {
			p.next()
			p.skipInline()
		}
	}
	p.next() // ]
	return list, nil
}

// skipInline consumes the few whitespace-only tokens that may appear inside a
// list literal between items.
func (p *parser) skipInline() {
	for {
		t := p.peek()
		if t.Kind == lexer.Newline {
			p.next()
			continue
		}
		return
	}
}

// parseSegments splits a raw string into literal / $(_var) / $(prop) segments.
func parseSegments(s string) ast.StringLit {
	lit := ast.StringLit{}
	for {
		idx := strings.Index(s, "$(")
		if idx < 0 {
			// No more interpolation markers: the remainder (if any) is text.
			if s != "" {
				lit.Segs = append(lit.Segs, ast.Seg{Text: s})
			}
			break
		}
		if idx > 0 {
			lit.Segs = append(lit.Segs, ast.Seg{Text: s[:idx]})
		}
		rest := s[idx+2:]
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			// Unclosed "$(" is treated as literal text rather than an error,
			// keeping the lexer/parser resilient to odd-but-harmless input.
			lit.Segs = append(lit.Segs, ast.Seg{Text: s[idx:]})
			break
		}
		path := rest[:end]
		// Underscore paths are script variables; everything else is a live
		// session property read.
		if strings.HasPrefix(path, "_") {
			lit.Segs = append(lit.Segs, ast.Seg{Var: path})
		} else {
			lit.Segs = append(lit.Segs, ast.Seg{Prop: path})
		}
		s = rest[end+1:]
	}
	// An empty string (e.g. "") must still produce exactly one empty text
	// segment so it interpolates to "" rather than nothing.
	if len(lit.Segs) == 0 {
		lit.Segs = []ast.Seg{{Text: ""}}
	}
	return lit
}

// isKeyword reports whether text is a reserved keyword that cannot be used as
// a function name.
func isKeyword(text string) bool {
	switch text {
	case "set", "get", "on", "start", "run", "off", "stop", "wait", "sleep",
		"echo", "say", "print", "show", "exec", "run.cmd", "command", "report",
		"if", "else", "elif", "end", "for", "each", "in", "repeat", "times",
		"while", "break", "continue", "true", "false", "not", "is", "contains",
		"try", "catch", "def", "return":
		return true
	}
	return false
}

// parseTry parses: try ... catch _err ... end
func (p *parser) parseTry() (ast.Stmt, error) {
	p.next() // try
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	body, term, err := p.parseBlock(false, true)
	if err != nil {
		return nil, err
	}
	if term != "catch" {
		return nil, parseError(p.peek(), "expected 'catch', found %s", p.peek())
	}
	p.next() // catch
	// Optional error variable name
	var catchVar string
	if p.at(lexer.Ident) && strings.HasPrefix(p.peek().Text, "_") {
		catchVar = p.peek().Text
		p.next()
	}
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	catchBody, term2, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	if term2 != "end" {
		return nil, parseError(p.peek(), "expected 'end', found %s", p.peek())
	}
	p.next()
	return ast.TryStmt{Body: body, CatchVar: catchVar, CatchBody: catchBody}, nil
}

// parseFuncDef parses: def funcname(_param1, _param2) ... end
func (p *parser) parseFuncDef() (ast.Stmt, error) {
	p.next() // def
	name, err := p.expect(lexer.Ident, "a function name")
	if err != nil {
		return nil, err
	}
	// Parse optional parameter list: ( _p1, _p2 )
	var params []string
	if p.at(lexer.LParen) {
		p.next() // (
		for !p.at(lexer.RParen) && !p.at(lexer.EOF) {
			if p.at(lexer.Comma) {
				p.next()
				continue
			}
			param, err := p.expect(lexer.Ident, "a parameter name starting with '_'")
			if err != nil {
				return nil, err
			}
			if !strings.HasPrefix(param.Text, "_") {
				return nil, parseError(param, "parameter must start with '_'")
			}
			params = append(params, param.Text)
		}
		if _, err := p.expect(lexer.RParen, "')'"); err != nil {
			return nil, err
		}
	}
	if err := p.endOfLine(); err != nil {
		return nil, err
	}
	body, term, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	if term != "end" {
		return nil, parseError(p.peek(), "expected 'end', found %s", p.peek())
	}
	p.next()
	return ast.FuncDefStmt{Name: name.Text, Params: params, Body: body}, nil
}

// parseCall parses a function call with arguments.
func (p *parser) parseCall() (ast.Stmt, error) {
	name := p.next() // function name
	var args []ast.Expr
	for !p.at(lexer.Newline) && !p.at(lexer.EOF) {
		if p.at(lexer.Comma) {
			p.next()
			continue
		}
		e, err := p.parseExprValue()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
	}
	return ast.CallStmt{Name: name.Text, Args: args}, nil
}

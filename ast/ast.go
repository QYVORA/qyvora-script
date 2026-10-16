// Package ast defines the syntax tree for the shared qyvora-script language.
//
// The node shapes are lifted directly from TOHA3EE's original internal/script
// AST (which this engine generalizes) so existing scripts parse identically.
package ast

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Stmt is a single executable statement in a script.
type Stmt interface {
	// stmt is a sealed-trait marker: it returns a tag so the engine can
	// type-switch on the concrete statement without a dummy value.
	stmt() string
}

// Program is the root of a parsed script.
type Program struct {
	Stmts []Stmt // the top-level statements, in source order
}

// -- expressions -----------------------------------------------------------

// Expr is a value-producing expression.
type Expr interface{ expr() string }

// StringLit is a string that may carry $(...) interpolation segments.
type StringLit struct {
	Segs []Seg // literal text / $(_var) / $(prop) pieces, concatenated in order
}

// Seg is one piece of a StringLit: either literal text, a variable reference
// or a live session property read.
type Seg struct {
	Text string // literal text (used when Var and Prop are both empty)
	Var  string // non-empty: "$(_name)" reference
	Prop string // non-empty: "$(path)" session property
}

// NumLit is a numeric literal.
type NumLit struct{ Value string }

// IdentExpr references a script variable.
type IdentExpr struct{ Name string }

// ListExpr is a bracketed list literal: [a, b, c].
type ListExpr struct{ Items []Expr }

// PropExpr is a bare $(path) session property read.
type PropExpr struct{ Path string }

// ArithExpr performs arithmetic on two values.
type ArithExpr struct {
	Op    string // +, -, *, /
	Left  Expr
	Right Expr
}

// expr() marker methods give the parser and engine a stable tag for each
// concrete expression type (used by Describe and the dry-run renderer).
func (StringLit) expr() string { return "string" }
func (NumLit) expr() string    { return "number" }
func (IdentExpr) expr() string { return "variable" }
func (ListExpr) expr() string  { return "list" }
func (PropExpr) expr() string  { return "property" }
func (ArithExpr) expr() string { return "arithmetic" }

// -- conditions ------------------------------------------------------------

// Cond is a boolean condition.
type Cond interface{ cond() string }

// BoolCond is a literal true/false.
type BoolCond struct{ Value bool }

// CmpCond compares two operands.
type CmpCond struct {
	Op string // ==, !=, <, >, <=, >=
	L  Expr
	R  Expr
}

// ContainsCond tests substring / list membership.
type ContainsCond struct{ L, R Expr }

// RunningCond tests whether a module is (not) running.
type RunningCond struct {
	Module Expr
	Negate bool // true for "is not running"
}

// NotCond negates a condition.
type NotCond struct{ C Cond }

// AndCond combines two conditions with logical AND.
type AndCond struct{ L, R Cond }

// OrCond combines two conditions with logical OR.
type OrCond struct{ L, R Cond }

// cond() marker methods tag each concrete condition for the dry-run renderer.
func (BoolCond) cond() string     { return "bool" }
func (CmpCond) cond() string      { return "comparison" }
func (ContainsCond) cond() string { return "contains" }
func (RunningCond) cond() string  { return "running" }
func (NotCond) cond() string      { return "not" }
func (AndCond) cond() string      { return "and" }
func (OrCond) cond() string       { return "or" }

// -- statements ------------------------------------------------------------

// AssignStmt sets or appends to a script variable.
type AssignStmt struct {
	Var   string
	Op    string // "=", "->" or ">>"
	Value Expr
}

// SetStmt writes a module configuration key ("set module.key -> value").
type SetStmt struct {
	Key   string
	Value Expr
}

// GetStmt reads a module configuration key into a variable.
type GetStmt struct {
	Key string
	Var string
}

// StartStmt starts a module, optionally with inline "key value" options.
type StartStmt struct {
	ID   string
	Opts []Expr // flattened "key value key value" option tokens
}

// StopStmt stops a running module.
type StopStmt struct{ ID string }

// WaitStmt blocks until a module finishes (wait for <module> [max <secs>]).
type WaitStmt struct {
	Module string
	Max    time.Duration
}

// SleepStmt pauses the script.
type SleepStmt struct{ Seconds Expr }

// EchoStmt prints a value.
type EchoStmt struct{ Value Expr }

// ShowStmt prints module metadata.
type ShowStmt struct{ ID string }

// ReportStmt writes a session report.
type ReportStmt struct{ Path Expr }

// ExecStmt runs a one-shot command line verbatim (gated shell-out).
type ExecStmt struct{ Raw string }

// ElifCond is one branch of a chained if/elif/else.
type ElifCond struct {
	Cond Cond
	Body []Stmt
}

// IfStmt is a conditional block with optional elif/else chains.
type IfStmt struct {
	Cond  Cond
	Then  []Stmt
	Elifs []ElifCond // elif branches, evaluated in order
	Else  []Stmt     // may be empty when there is no else clause
}

// ForEachStmt iterates a list.
type ForEachStmt struct {
	Var  string // loop variable name (must start with '_')
	List Expr
	Body []Stmt
}

// RepeatStmt repeats a block N times.
type RepeatStmt struct {
	N    Expr // number of iterations (a numeric expression)
	Body []Stmt
}

// WhileStmt loops while a condition holds.
type WhileStmt struct {
	Cond Cond
	Body []Stmt
}

// BreakStmt exits the innermost loop.
type BreakStmt struct{}

// ContinueStmt jumps to the next loop iteration.
type ContinueStmt struct{}

// HaltStmt stops the whole script.
type HaltStmt struct{}

// TryStmt wraps a block and catches errors, assigning the error message to a variable.
type TryStmt struct {
	Body      []Stmt
	CatchVar  string // variable name to store error message (e.g. "_err")
	CatchBody []Stmt // executed when an error occurs
}

// FuncDefStmt defines a named function with parameters.
type FuncDefStmt struct {
	Name   string   // function name (without _ prefix)
	Params []string // parameter names (must start with _)
	Body   []Stmt
}

// CallStmt calls a named function.
type CallStmt struct {
	Name string // function name
	Args []Expr // arguments to pass to parameters
}

// stmt() marker methods tag each concrete statement for the engine switch and
// the dry-run renderer.
func (AssignStmt) stmt() string   { return "assign" }
func (SetStmt) stmt() string      { return "set" }
func (GetStmt) stmt() string      { return "get" }
func (StartStmt) stmt() string    { return "start" }
func (StopStmt) stmt() string     { return "stop" }
func (WaitStmt) stmt() string     { return "wait" }
func (SleepStmt) stmt() string    { return "sleep" }
func (EchoStmt) stmt() string     { return "echo" }
func (ShowStmt) stmt() string     { return "show" }
func (ExecStmt) stmt() string     { return "exec" }
func (ReportStmt) stmt() string   { return "report" }
func (IfStmt) stmt() string       { return "if" }
func (ForEachStmt) stmt() string  { return "for-each" }
func (RepeatStmt) stmt() string   { return "repeat" }
func (WhileStmt) stmt() string    { return "while" }
func (BreakStmt) stmt() string    { return "break" }
func (ContinueStmt) stmt() string { return "continue" }
func (HaltStmt) stmt() string     { return "halt" }
func (TryStmt) stmt() string      { return "try" }
func (FuncDefStmt) stmt() string  { return "func-def" }
func (CallStmt) stmt() string     { return "call" }

// Describe renders a parsed program as an English summary. It powers the
// `build <file>` dry-run so users can review a script before running.
func Describe(prog *Program) []string {
	var out []string
	// walk recursively descends nested blocks, indenting by depth so the plan
	// reads like the source layout it came from.
	var walk func(stmts []Stmt, depth int)
	indent := func(depth int) string { return strings.Repeat("  ", depth) }
	walk = func(stmts []Stmt, depth int) {
		for _, s := range stmts {
			switch st := s.(type) {
			case SetStmt:
				out = append(out, fmt.Sprintf("%sset %s = %s", indent(depth), st.Key, ExprText(st.Value)))
			case GetStmt:
				out = append(out, fmt.Sprintf("%s%s <- get %s", indent(depth), st.Var, st.Key))
			case AssignStmt:
				out = append(out, fmt.Sprintf("%s%s %s %s", indent(depth), st.Var, st.Op, ExprText(st.Value)))
			case StartStmt:
				out = append(out, fmt.Sprintf("%srun %s", indent(depth), st.ID))
			case StopStmt:
				out = append(out, fmt.Sprintf("%sstop %s", indent(depth), st.ID))
			case WaitStmt:
				out = append(out, fmt.Sprintf("%swait for %s to finish", indent(depth), st.Module))
			case SleepStmt:
				out = append(out, fmt.Sprintf("%ssleep %s second(s)", indent(depth), ExprText(st.Seconds)))
			case EchoStmt:
				out = append(out, fmt.Sprintf("%sprint %s", indent(depth), ExprText(st.Value)))
			case ShowStmt:
				out = append(out, fmt.Sprintf("%sshow module %s", indent(depth), st.ID))
			case ExecStmt:
				out = append(out, fmt.Sprintf("%sexec %s", indent(depth), st.Raw))
			case ReportStmt:
				out = append(out, fmt.Sprintf("%swrite report to %s", indent(depth), ExprText(st.Path)))
			case IfStmt:
				out = append(out, fmt.Sprintf("%sif %s", indent(depth), CondText(st.Cond)))
				walk(st.Then, depth+1)
				for _, elif := range st.Elifs {
					out = append(out, fmt.Sprintf("%selif %s", indent(depth), CondText(elif.Cond)))
					walk(elif.Body, depth+1)
				}
				// The else clause is only shown when the script actually has one.
				if len(st.Else) > 0 {
					out = append(out, indent(depth)+"else")
					walk(st.Else, depth+1)
				}
				out = append(out, indent(depth)+"end")
			case ForEachStmt:
				out = append(out, fmt.Sprintf("%sfor each %s in %s", indent(depth), st.Var, ExprText(st.List)))
				walk(st.Body, depth+1)
				out = append(out, indent(depth)+"end")
			case RepeatStmt:
				out = append(out, fmt.Sprintf("%srepeat %s times", indent(depth), ExprText(st.N)))
				walk(st.Body, depth+1)
				out = append(out, indent(depth)+"end")
			case WhileStmt:
				out = append(out, fmt.Sprintf("%swhile %s", indent(depth), CondText(st.Cond)))
				walk(st.Body, depth+1)
				out = append(out, indent(depth)+"end")
			case BreakStmt:
				out = append(out, indent(depth)+"break")
			case ContinueStmt:
				out = append(out, indent(depth)+"continue")
			case HaltStmt:
				out = append(out, indent(depth)+"stop the script")
			case TryStmt:
				out = append(out, indent(depth)+"try")
				walk(st.Body, depth+1)
				catchVar := ""
				if st.CatchVar != "" {
					catchVar = " " + st.CatchVar
				}
				out = append(out, fmt.Sprintf("%scatch%s", indent(depth), catchVar))
				walk(st.CatchBody, depth+1)
				out = append(out, indent(depth)+"end")
			case FuncDefStmt:
				params := strings.Join(st.Params, ", ")
				out = append(out, fmt.Sprintf("%sdef %s(%s)", indent(depth), st.Name, params))
				walk(st.Body, depth+1)
				out = append(out, indent(depth)+"end")
			case CallStmt:
				args := make([]string, 0, len(st.Args))
				for _, a := range st.Args {
					args = append(args, ExprText(a))
				}
				out = append(out, fmt.Sprintf("%s%s(%s)", indent(depth), st.Name, strings.Join(args, ", ")))
			}
		}
	}
	walk(prog.Stmts, 0)
	return out
}

// ExprText renders an expression back to readable source for the dry-run.
func ExprText(e Expr) string {
	switch v := e.(type) {
	case StringLit:
		return v.StrText()
	case NumLit:
		return v.Value
	case IdentExpr:
		return v.Name
	case PropExpr:
		return "$(" + v.Path + ")"
	case ListExpr:
		parts := make([]string, 0, len(v.Items))
		for _, it := range v.Items {
			parts = append(parts, ExprText(it))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case ArithExpr:
		return fmt.Sprintf("%s %s %s", ExprText(v.Left), v.Op, ExprText(v.Right))
	}
	return "?"
}

// StrText rebuilds the original string form of a StringLit, re-quoting it so
// interpolation markers survive the round trip.
func (s StringLit) StrText() string {
	var b strings.Builder
	for _, seg := range s.Segs {
		switch {
		case seg.Var != "":
			b.WriteString("$(" + seg.Var + ")")
		case seg.Prop != "":
			b.WriteString("$(" + seg.Prop + ")")
		default:
			b.WriteString(seg.Text)
		}
	}
	return strconv.Quote(b.String())
}

// CondText renders a condition back to readable source for the dry-run.
func CondText(c Cond) string {
	switch v := c.(type) {
	case BoolCond:
		return strconv.FormatBool(v.Value)
	case CmpCond:
		return fmt.Sprintf("%s %s %s", ExprText(v.L), v.Op, ExprText(v.R))
	case ContainsCond:
		return fmt.Sprintf("%s contains %s", ExprText(v.L), ExprText(v.R))
	case RunningCond:
		neg := ""
		if v.Negate {
			neg = "not "
		}
		return fmt.Sprintf("%s is %srunning", ExprText(v.Module), neg)
	case NotCond:
		return "not " + CondText(v.C)
	case AndCond:
		return CondText(v.L) + " && " + CondText(v.R)
	case OrCond:
		return CondText(v.L) + " || " + CondText(v.R)
	}
	return "?"
}

// NumOf extracts a number from a literal expression, when the expression is a
// plain string or number literal.
func NumOf(e Expr) (float64, bool) {
	var s string
	switch v := e.(type) {
	case StringLit:
		// Only a pure text segment (no Var/Prop) can be a number; any
		// interpolation means the value is dynamic and must be resolved at
		// runtime instead.
		if len(v.Segs) != 1 || v.Segs[0].Var != "" || v.Segs[0].Prop != "" {
			return 0, false
		}
		s = v.Segs[0].Text
	case NumLit:
		s = v.Value
	case IdentExpr:
		// Variable references cannot be resolved at parse time.
		return 0, false
	default:
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

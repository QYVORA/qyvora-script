// Package interpreter evaluates a parsed qyvora-script program against a
// binding.Binding.
//
// It is generalized from TOHA3EE's original internal/script/engine.go: the
// evaluation semantics, control flow and interpolation rules are unchanged.
// The only structural difference is that every host interaction (module
// lifecycle, config, properties, one-shot commands) is dispatched through
// binding.Binding instead of a tool-specific Runner, and exec/run.cmd is gated
// behind Binding.AllowShellOut.
package interpreter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QYVORA/qyvora-script/ast"
	"github.com/QYVORA/qyvora-script/binding"
	"github.com/QYVORA/qyvora-script/parser"
)

// sentinel errors that drive control flow inside a script.
var (
	errBreak    = errors.New("script: break")
	errContinue = errors.New("script: continue")
	errStop     = errors.New("script: stop")
)

// maxLoop is the safety cap on while-loop iterations so a bad condition can
// never hang a script forever.
const maxLoop = 1000000

// AllowShellOutEnv is the environment variable a host tool may set (instead of
// passing a flag) to permit exec/run.cmd. A binding's AllowShellOut may consult
// it; the engine only reports its name in the refusal error.
const AllowShellOutEnv = "QYVORA_ALLOW_SHELLOUT"

// Engine evaluates a parsed Program against a Binding.
type Engine struct {
	Binding binding.Binding
	// Events, when non-nil, receives a JSONL line for each shell-out that
	// actually executes. It is optional; a binding may also implement
	// binding.EventSink to record the same event in its own audit trail.
	Events io.Writer

	vars  map[string]value
	funcs map[string]*ast.FuncDefStmt
}

// NewEngine returns an engine that drives b.
func NewEngine(b binding.Binding) *Engine {
	return &Engine{Binding: b, vars: map[string]value{}}
}

// value is a script runtime value: either a scalar string or a list.
type value struct {
	s      string
	list   []string
	isList bool // when true, the list field is authoritative
}

// str renders a value as a single string: lists join with ", ".
func (v value) str() string {
	if v.isList {
		return strings.Join(v.list, ", ")
	}
	return v.s
}

// Run parses and executes src.
func (e *Engine) Run(src string) error {
	prog, err := parser.Parse(src)
	if err != nil {
		return err
	}
	return e.RunProgram(prog)
}

// RunFile reads and executes a ".qs" script file.
func (e *Engine) RunFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("script: read %s: %w", path, err)
	}
	return e.Run(string(data))
}

// RunProgram executes a parsed program.
func (e *Engine) RunProgram(prog *ast.Program) error {
	for _, stmt := range prog.Stmts {
		if err := e.evalStmt(stmt); err != nil {
			// Halt is the script's own "stop now" signal and is a clean exit.
			if errors.Is(err, errStop) {
				return nil
			}
			// break/continue escaping the top level are authoring mistakes;
			// surface them as errors instead of silently swallowing them.
			if errors.Is(err, errBreak) || errors.Is(err, errContinue) {
				return fmt.Errorf("script: %w outside of a loop", err)
			}
			return err
		}
	}
	return nil
}

func (e *Engine) evalStmt(s ast.Stmt) error {
	switch st := s.(type) {
	case ast.AssignStmt:
		return e.assign(st)

	case ast.SetStmt:
		// Evaluate the value expression first so variables and $(...) props
		// are interpolated before writing to the config store.
		val := e.evalExpr(st.Value)
		if _, err := e.call(binding.CapSetConfig, st.Key, val.str()); err != nil {
			return fmt.Errorf("script: set %s: %w", st.Key, err)
		}
		return nil

	case ast.GetStmt:
		// A config read always lands as a scalar string variable.
		res, _ := e.call(binding.CapGetConfig, st.Key)
		e.vars[st.Var] = value{s: asString(res)}
		return nil

	case ast.StartStmt:
		// Inline "key value" tokens are flattened into a real options map.
		opts, err := evalOpts(e, st.Opts)
		if err != nil {
			return err
		}
		if _, err := e.call(binding.CapStart, st.ID, opts); err != nil {
			return fmt.Errorf("script: %s: %w", st.ID, err)
		}
		return nil

	case ast.StopStmt:
		if _, err := e.call(binding.CapStop, st.ID); err != nil {
			return fmt.Errorf("script: off %s: %w", st.ID, err)
		}
		return nil

	case ast.WaitStmt:
		return e.wait(st)

	case ast.SleepStmt:
		secs, ok := numFromValue(e.evalExpr(st.Seconds))
		if !ok {
			return fmt.Errorf("script: sleep needs a number of seconds")
		}
		// Convert the float seconds to a Duration by scaling against the
		// time.Second constant.
		time.Sleep(time.Duration(secs * float64(time.Second)))
		return nil

	case ast.EchoStmt:
		if _, err := e.call(binding.CapEcho, e.evalExpr(st.Value).str()); err != nil {
			return fmt.Errorf("script: echo: %w", err)
		}
		return nil

	case ast.ShowStmt:
		if _, err := e.call(binding.CapShow, st.ID); err != nil {
			return fmt.Errorf("script: show %s: %w", st.ID, err)
		}
		return nil

	case ast.ExecStmt:
		return e.exec(st)

	case ast.ReportStmt:
		if _, err := e.call(binding.CapReport, e.evalExpr(st.Path).str()); err != nil {
			return fmt.Errorf("script: report: %w", err)
		}
		return nil

	case ast.IfStmt:
		if e.evalCond(st.Cond) {
			return e.evalBody(st.Then)
		}
		for _, elif := range st.Elifs {
			if e.evalCond(elif.Cond) {
				return e.evalBody(elif.Body)
			}
		}
		return e.evalBody(st.Else)

	case ast.ForEachStmt:
		return e.forEach(st)

	case ast.RepeatStmt:
		n, ok := numFromValue(e.evalExpr(st.N))
		if !ok {
			return fmt.Errorf("script: repeat needs a number")
		}
		// Break exits the whole loop; continue moves to the next iteration;
		// anything else is a real error that stops the script.
		for i := 0; i < int(n); i++ {
			if err := e.evalBody(st.Body); err != nil {
				if errors.Is(err, errBreak) {
					return nil
				}
				if errors.Is(err, errContinue) {
					continue
				}
				return err
			}
		}
		return nil

	case ast.WhileStmt:
		iterations := 0
		// The maxLoop guard protects against an always-true condition.
		for e.evalCond(st.Cond) {
			iterations++
			if iterations > maxLoop {
				return fmt.Errorf("script: while loop exceeded %d iterations", maxLoop)
			}
			if err := e.evalBody(st.Body); err != nil {
				if errors.Is(err, errBreak) {
					return nil
				}
				if errors.Is(err, errContinue) {
					continue
				}
				return err
			}
		}
		return nil

	case ast.BreakStmt:
		return errBreak

	case ast.ContinueStmt:
		return errContinue

	case ast.HaltStmt:
		return errStop

	case ast.TryStmt:
		return e.evalTry(st)

	case ast.FuncDefStmt:
		// Store the function definition in the engine's function table.
		if e.funcs == nil {
			e.funcs = map[string]*ast.FuncDefStmt{}
		}
		// Copy to avoid aliasing issues
		fd := st
		e.funcs[st.Name] = &fd
		return nil

	case ast.CallStmt:
		return e.evalCall(st)
	}
	return fmt.Errorf("script: unknown statement %T", s)
}

// call dispatches a capability invocation through the binding, returning the
// raw result for the caller to render.
func (e *Engine) call(capabilityID string, args ...binding.Value) (binding.Value, error) {
	if e.Binding == nil {
		return nil, fmt.Errorf("script: no binding configured")
	}
	return e.Binding.Call(capabilityID, args)
}

// exec runs a gated shell-out. The permission gate lives in the binding; the
// interpreter refuses when AllowShellOut reports false and records a JSONL
// event (plus an optional binding audit event) when a shell-out actually runs.
func (e *Engine) exec(st ast.ExecStmt) error {
	if e.Binding == nil || !e.Binding.AllowShellOut() {
		return fmt.Errorf("script: exec %q requires shell-out to be enabled; pass --allow-shellout or set %s=1", st.Raw, AllowShellOutEnv)
	}
	if _, err := e.call(binding.CapExec, st.Raw); err != nil {
		return fmt.Errorf("script: %s: %w", st.Raw, err)
	}
	e.emitShellOut(st.Raw)
	return nil
}

// emitShellOut records the shell-out. It writes one JSONL line to Events (when
// configured) and notifies the binding if it implements binding.EventSink.
func (e *Engine) emitShellOut(line string) {
	if e.Events != nil {
		payload := map[string]string{"event": "script.shellout", "command": line}
		if enc, err := json.Marshal(payload); err == nil {
			_, _ = e.Events.Write(append(enc, '\n'))
		}
	}
	if sink, ok := e.Binding.(binding.EventSink); ok {
		sink.AuditEvent("script.shellout", line)
	}
}

// evalBody evaluates a block, translating break/continue/stop into the caller.
func (e *Engine) evalBody(stmts []ast.Stmt) error {
	for _, stmt := range stmts {
		if err := e.evalStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) assign(st ast.AssignStmt) error {
	val := e.evalExpr(st.Value)
	switch st.Op {
	case ">>":
		// Append: promote to a list on first use, then extend the existing
		// list in place.
		cur, ok := e.vars[st.Var]
		if !ok || !cur.isList {
			e.vars[st.Var] = value{isList: true, list: []string{val.str()}}
			return nil
		}
		cur.list = append(cur.list, val.str())
		e.vars[st.Var] = cur
		return nil
	default:
		// "=" and "->" both overwrite the variable with the new value.
		e.vars[st.Var] = val
		return nil
	}
}

func (e *Engine) wait(st ast.WaitStmt) error {
	// Poll IsRunning at a coarse interval so a short wait does not spin the
	// CPU; the caller-supplied Max bounds how long we wait.
	deadline := time.Now().Add(st.Max)
	for {
		if !e.isRunning(st.Module) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("script: wait for %s timed out", st.Module)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// isRunning asks the binding whether a module is live.
func (e *Engine) isRunning(id string) bool {
	res, err := e.call(binding.CapIsRunning, id)
	if err != nil {
		return false
	}
	return asBool(res)
}

func (e *Engine) forEach(st ast.ForEachStmt) error {
	v := e.evalExpr(st.List)
	var items []string
	if v.isList {
		items = v.list
	} else {
		// A scalar source is split on commas so "a,b,c" iterates too; empty
		// pieces from trailing/leading commas are dropped.
		for _, part := range strings.Split(v.s, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				items = append(items, part)
			}
		}
	}
	for _, item := range items {
		e.vars[st.Var] = value{s: item}
		if err := e.evalBody(st.Body); err != nil {
			if errors.Is(err, errBreak) {
				return nil
			}
			if errors.Is(err, errContinue) {
				continue
			}
			return err
		}
	}
	return nil
}

// evalTry executes a try/catch block, catching errors and assigning them to a variable.
func (e *Engine) evalTry(st ast.TryStmt) error {
	err := e.evalBody(st.Body)
	if err == nil {
		return nil
	}
	// Store the error message in the catch variable if provided
	if st.CatchVar != "" {
		e.vars[st.CatchVar] = value{s: err.Error()}
	}
	// Execute the catch body (which may handle the error)
	return e.evalBody(st.CatchBody)
}

// evalCall executes a function call.
func (e *Engine) evalCall(st ast.CallStmt) error {
	fd, ok := e.funcs[st.Name]
	if !ok {
		return fmt.Errorf("script: undefined function %q", st.Name)
	}
	// Save current scope and create a new one for the function
	savedVars := e.vars
	e.vars = map[string]value{}
	// Copy outer variables that start with _ (script convention for shared state)
	for k, v := range savedVars {
		if strings.HasPrefix(k, "_") {
			e.vars[k] = v
		}
	}
	// Bind arguments to parameters
	for i, param := range fd.Params {
		if i < len(st.Args) {
			e.vars[param] = e.evalExpr(st.Args[i])
		} else {
			e.vars[param] = value{}
		}
	}
	// Execute the function body
	err := e.evalBody(fd.Body)
	// Restore the outer scope, but keep any new _ variables set by the function
	outerVars := savedVars
	for k, v := range e.vars {
		if strings.HasPrefix(k, "_") {
			outerVars[k] = v
		}
	}
	e.vars = outerVars
	return err
}

// evalExpr resolves an expression to a runtime value.
func (e *Engine) evalExpr(x ast.Expr) value {
	switch ex := x.(type) {
	case ast.StringLit:
		return value{s: e.interpolate(ex)}
	case ast.NumLit:
		// Numbers stay textual; they are parsed to float only when compared
		// or used as a repeat/sleep count.
		return value{s: ex.Value}
	case ast.IdentExpr:
		// An unknown variable reads as the empty string instead of an error,
		// which keeps conditions like "if $(_missing) == ''" working.
		if v, ok := e.vars[ex.Name]; ok {
			return v
		}
		return value{s: ""}
	case ast.PropExpr:
		return e.resolveProp(ex.Path)
	case ast.ListExpr:
		var out []string
		for _, item := range ex.Items {
			out = append(out, e.evalExpr(item).str())
		}
		return value{isList: true, list: out}
	case ast.ArithExpr:
		return e.evalArith(ex)
	}
	return value{}
}

// interpolate builds the final string from the segments of a StringLit.
func (e *Engine) interpolate(lit ast.StringLit) string {
	var b strings.Builder
	for _, seg := range lit.Segs {
		switch {
		case seg.Var != "":
			b.WriteString(e.varValue(seg.Var).str())
		case seg.Prop != "":
			b.WriteString(e.resolveProp(seg.Prop).str())
		default:
			b.WriteString(seg.Text)
		}
	}
	return b.String()
}

func (e *Engine) varValue(name string) value {
	// $(list.size) resolves to the length of a list variable.
	if strings.HasSuffix(name, ".size") {
		base := name[:len(name)-len(".size")]
		if v, ok := e.vars[base]; ok {
			if v.isList {
				return value{s: strconv.Itoa(len(v.list))}
			}
			// A scalar's ".size" reports its character count.
			return value{s: strconv.Itoa(len(v.s))}
		}
	}
	if v, ok := e.vars[name]; ok {
		return v
	}
	return value{}
}

// resolveProp handles $(path): script variables take priority when the path
// starts with an underscore, otherwise the Binding resolves a tool property.
func (e *Engine) resolveProp(path string) value {
	if strings.HasPrefix(path, "_") {
		return e.varValue(path)
	}
	if e.Binding != nil {
		if res, err := e.call(binding.CapProp, path); err == nil && res != nil {
			return value{s: asString(res)}
		}
	}
	// Unknown properties read as empty, mirroring unknown variables.
	return value{}
}

// evalCond evaluates a condition expression.
func (e *Engine) evalCond(c ast.Cond) bool {
	switch cond := c.(type) {
	case ast.BoolCond:
		return cond.Value
	case ast.CmpCond:
		return cmpValues(cond.Op, e.evalExpr(cond.L), e.evalExpr(cond.R))
	case ast.ContainsCond:
		l, r := e.evalExpr(cond.L), e.evalExpr(cond.R)
		// A list on the left means membership; a scalar means substring.
		if l.isList {
			for _, item := range l.list {
				if item == r.str() {
					return true
				}
			}
			return false
		}
		return strings.Contains(l.str(), r.str())
	case ast.RunningCond:
		mod := e.evalExpr(cond.Module).str()
		// Without a binding nothing can be running.
		running := e.Binding != nil && e.isRunning(mod)
		if cond.Negate {
			return !running
		}
		return running
	case ast.NotCond:
		return !e.evalCond(cond.C)
	case ast.AndCond:
		// Short-circuit evaluation: the right side only runs when needed.
		return e.evalCond(cond.L) && e.evalCond(cond.R)
	case ast.OrCond:
		return e.evalCond(cond.L) || e.evalCond(cond.R)
	}
	return false
}

// cmpValues compares two values; numeric when both parse as numbers.
func cmpValues(op string, l, r value) bool {
	ls, rs := l.str(), r.str()
	// If both sides parse as floats the comparison is numeric ("10" > "9");
	// otherwise fall through to a plain string comparison.
	if ln, lok := strconv.ParseFloat(ls, 64); lok == nil {
		if rn, rok := strconv.ParseFloat(rs, 64); rok == nil {
			return cmpNums(op, ln, rn)
		}
	}
	switch op {
	case "==":
		return ls == rs
	case "!=":
		return ls != rs
	case "<":
		return ls < rs
	case ">":
		return ls > rs
	case "<=":
		return ls <= rs
	case ">=":
		return ls >= rs
	}
	return false
}

// evalArith performs arithmetic on two values.
func (e *Engine) evalArith(ex ast.ArithExpr) value {
	l := e.evalExpr(ex.Left)
	r := e.evalExpr(ex.Right)
	ln, lok := numFromValue(l)
	rn, rok := numFromValue(r)
	if !lok || !rok {
		return value{s: "0"}
	}
	var result float64
	switch ex.Op {
	case "+":
		result = ln + rn
	case "-":
		result = ln - rn
	case "*":
		result = ln * rn
	case "/":
		if rn == 0 {
			return value{s: "0"}
		}
		result = ln / rn
	}
	return value{s: strconv.FormatFloat(result, 'f', -1, 64)}
}

func cmpNums(op string, l, r float64) bool {
	switch op {
	case "==":
		return l == r
	case "!=":
		return l != r
	case "<":
		return l < r
	case ">":
		return l > r
	case "<=":
		return l <= r
	case ">=":
		return l >= r
	}
	return false
}

// evalOpts flattens the inline "key value key value" tokens of a StartStmt.
func evalOpts(e *Engine, exprs []ast.Expr) (map[string]string, error) {
	opts := map[string]string{}
	for i := 0; i+1 < len(exprs); i += 2 {
		key := e.evalExpr(exprs[i]).str()
		val := e.evalExpr(exprs[i+1]).str()
		if key == "" {
			return nil, fmt.Errorf("script: module option key missing before %q", val)
		}
		opts[key] = val
	}
	// A trailing lone token means the user forgot a value.
	if len(exprs)%2 != 0 {
		return nil, fmt.Errorf("script: module options must come in key/value pairs")
	}
	return opts, nil
}

// asString renders a binding result as the scalar string the interpreter uses.
func asString(v binding.Value) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []string:
		return strings.Join(x, ", ")
	case binding.ListValue:
		return strings.Join([]string(x), ", ")
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

// asBool coerces a binding result to a boolean, accepting bool or the usual
// string spellings.
func asBool(v binding.Value) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		return err == nil && b
	default:
		b, err := strconv.ParseBool(strings.TrimSpace(asString(v)))
		return err == nil && b
	}
}

// numFromValue extracts a number from a runtime value.
func numFromValue(v value) (float64, bool) {
	// Lists are never numeric; a non-numeric scalar reports not-ok so the
	// caller can raise a descriptive error.
	if v.isList {
		return 0, false
	}
	n, err := strconv.ParseFloat(v.s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

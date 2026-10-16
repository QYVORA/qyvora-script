// Package binding defines the interface a host tool implements so that a
// qyvora-script program can drive it.
//
// The engine is deliberately tool-agnostic: every module-lifecycle operation,
// property read and one-shot command in a script is dispatched through
// Binding.Call using one of the reserved capability IDs declared here. A tool's
// binding translates those calls into its own session, module registry or CLI
// dispatch, which is what lets one language serve every QYVORA framework.
package binding

// Value is a script value passed to and returned from a Binding. The
// interpreter works with string and []string at runtime, but a binding may use
// any type that renders to a string; ListValue builds the common list case.
type Value interface{}

// Reserved capability IDs. The interpreter always calls a Binding with one of
// these IDs, so every binding must handle the ones its tool supports. The
// argument shapes are documented per constant.
const (
	// CapEcho prints a value to the console. args: [text string].
	CapEcho = "echo"

	// CapSetConfig writes a "module.key" configuration value.
	// args: [key string, value string].
	CapSetConfig = "set_config"

	// CapGetConfig reads a "module.key" configuration value.
	// args: [key string]. Returns the current value as a string.
	CapGetConfig = "get_config"

	// CapStart starts a module with inline options.
	// args: [id string, opts map[string]string].
	CapStart = "start"

	// CapStop stops a running module. args: [id string].
	CapStop = "stop"

	// CapIsRunning reports whether a module is live.
	// args: [id string]. Returns a bool.
	CapIsRunning = "is_running"

	// CapShow prints module metadata. args: [id string].
	CapShow = "show"

	// CapReport writes a session report. args: [path string].
	CapReport = "report"

	// CapProp resolves a live tool property for $(...) interpolation.
	// args: [name string]. Returns the value as a string, or nil when the
	// property is unknown (the interpreter renders that as the empty string).
	CapProp = "prop"

	// CapExec runs a one-shot command line captured verbatim from an
	// exec/run.cmd statement. args: [line string]. The interpreter only calls
	// this after AllowShellOut reports true, so the binding may dispatch it
	// directly.
	CapExec = "exec"
)

// Binding connects a running script to a host tool.
type Binding interface {
	// Call dispatches a capability invocation. capabilityID is one of the
	// reserved Cap* constants; args follow that constant's documented shape.
	Call(capabilityID string, args []Value) (Value, error)

	// Capabilities lists the capability IDs this binding supports. It is
	// informational (e.g. surfaced by `script capabilities`).
	Capabilities() []string

	// AllowShellOut gates exec/run.cmd. It must return false unless the host
	// tool was invoked with an explicit opt-in (a flag such as
	// --allow-shellout or a matching environment variable). When it returns
	// false the interpreter refuses to run exec/run.cmd and reports the flag
	// the user needs.
	AllowShellOut() bool
}

// EventSink is an optional interface a Binding may implement so the engine can
// record security-relevant events (currently shell-out) in the tool's own
// audit trail. When a binding implements it, the interpreter calls AuditEvent
// whenever an exec/run.cmd statement actually runs.
type EventSink interface {
	// AuditEvent records an event using the host tool's shared event
	// contract (for example a JSONL session log).
	AuditEvent(kind, detail string)
}

// ListValue is a convenience for bindings that want to return a list of
// strings from Call. The interpreter renders it as a comma-separated string.
type ListValue []string

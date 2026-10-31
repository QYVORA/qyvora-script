# qyvora-script

**Status:** `CURRENT` · **Version:** `v0.1.1` · **Not a security tool**
**Last verified against implementation:** 2026-10-16

`qyvora-script` is the shared scripting engine for the fourteen QYVORA
security frameworks. It is one language, not a per-tool variant: every
framework that wants to expose scripting implements one small interface
(`binding.Binding`) and gets the whole language.

It is one of four supporting repositories alongside `qyvora-common` (machine
contract), `qyvora-tui` (shared terminal UI) and `qyvora-dist` (distribution).
**None of them is a security framework.**

## What this module is

| | |
|---|---|
| Module path | `github.com/QYVORA/qyvora-script` |
| Go | `1.26` |
| Packages | `ast/`, `lexer/`, `parser/`, `interpreter/`, `binding/` |
| Script extension | `.qs` (the legacy `.toha3ee` extension is also accepted) |

The engine was **generalized from TOHA3EE's original `internal/script`
implementation** (3166 lines) rather than written from scratch, so the grammar,
token set and AST node shapes are exactly those that already powered TOHA3EE's
scripts. The only structural change is that all host interaction is dispatched
through `binding.Binding` instead of a tool-specific `Runner`, and the
`exec`/`run.cmd` shell-out construct is gated.

## Language

An English-like, line-oriented DSL. Full feature set:

- **Variables**: `set`/`get`, `->` / `=` assignment, `>>` list append, `$(...)`
  interpolation of variables and live tool properties
- **Module lifecycle**: `on`/`off`/`run`/`stop`/`wait for`/`sleep`, dispatched
  through the tool's binding
- **Control flow**: `if`/`elif`/`else`/`end`, `for each ... in`, `repeat N times`,
  `while`, `break`, `continue`
- **Error handling**: `try`/`catch`
- **User-defined functions**: `def name(_param) ... end`
- **Lists**: `[a, b, c]` literals and append
- **Shell-out**: `exec` / `run.cmd` / `command` — gated (see below)

See `examples/demo.qs` for a script that exercises every construct.

## Shell-out gating

`exec`/`run.cmd` is **allowed but not silently available**. It only succeeds
when the host tool was invoked with an explicit opt-in:

- a flag such as `--allow-shellout`, or
- the `QYVORA_ALLOW_SHELLOUT=1` environment variable.

By default the statement fails with a clear error naming the flag, and it never
runs silently. When a shell-out does execute, the engine writes a JSONL event
(`{"event":"script.shellout","command":"..."}`) and, if the binding implements
`binding.EventSink`, records it in the tool's own audit trail.

## Binding interface

A host tool implements three methods:

```go
type Binding interface {
    Call(capabilityID string, args []Value) (Value, error)
    Capabilities() []string
    AllowShellOut() bool
}
```

`capabilityID` is one of the reserved `binding.Cap*` constants (`CapEcho`,
`CapSetConfig`, `CapGetConfig`, `CapStart`, `CapStop`, `CapIsRunning`,
`CapShow`, `CapReport`, `CapProp`, `CapExec`); the argument shape for each is
documented on the constant.

## Integrating a framework

1. Add the module and a local replace directive:

   ```
   require github.com/QYVORA/qyvora-script v0.1.0
   replace github.com/QYVORA/qyvora-script => ../../shared/qyvora-script
   ```

2. Implement `binding.Binding` against the tool's session/capability registry.
3. Add a flat `script run <file>` subcommand (and, ideally, `script build`
   for a dry-run plan).
4. Ship one real example `.qs` script and confirm it executes.

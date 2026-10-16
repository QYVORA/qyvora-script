// Package qyvora_script is the root of the shared QYVORA scripting engine.
//
// The engine is an English-like, line-oriented DSL stored in ".qs" files. It
// was generalized from TOHA3EE's original internal/script implementation so
// that every QYVORA framework ships one scripting language instead of a
// per-tool variant. A script drives a tool's recon -> exploit -> report
// pipeline without manual command entry:
//
//	# a full pipeline, end to end
//	set net.scan.targets -> "192.168.8.0/24"
//	on net.scan
//	wait for net.scan
//	_hosts -> [$(net.hosts)]
//	echo -> "discovered $(_hosts.size) host(s)"
//
//	if $(hosts.count) > 0
//	    on arp.spoof targets $(_hosts)
//	    sleep -> 30
//	    off arp.spoof
//	end
//	report -> "assessment.md"
//
// The syntax is deliberately small: `->` / `=` assign, `>>` appends to a
// list, `_` prefixes variable names, `$(...)` interpolates live tool state,
// and the control flow reads like English (`for each`, `repeat N times`,
// `if`/`else`/`end`, `while`).
//
// The engine is tool-agnostic. Everything a script does to the host tool is
// dispatched through binding.Binding, so the same language runs against any
// framework that implements the interface.
//
// Layout:
//
//	ast/          — statements, expressions and conditions
//	lexer/        — tokenizer
//	parser/       — recursive-descent parser
//	interpreter/  — evaluator driving a binding.Binding
//	binding/      — the tool-integration interface
package qyvora_script

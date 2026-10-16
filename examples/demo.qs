# demo.qs — a self-contained example of the shared QYVORA scripting language.
#
# It exercises every construct the engine supports: variables, lists, module
# lifecycle, control flow, error handling, user-defined functions and a gated
# shell-out. Run it with a tool's `script run` subcommand, for example:
#
#     toha3ee script run examples/demo.qs
#
# The engine is tool-agnostic: `on`, `set`, `$(...)` properties and `exec`
# are dispatched through whichever tool's binding.Binding is driving it.

# --- variables and lists ---------------------------------------------------
set demo.targets -> "10.0.0.0/24"
_hosts -> []
_hosts >> "10.0.0.1"
_hosts >> "10.0.0.2"
echo -> "loaded $(_hosts.size) host(s): $(_hosts)"

# --- arithmetic ------------------------------------------------------------
_ports -> 80 + 443 / 1
echo -> "computed $(_ports)"

# --- a user-defined function ----------------------------------------------
def greet(_who)
    echo -> "hello $(_who)"
end

greet "recon-phase"

# --- conditionals ----------------------------------------------------------
if $(_hosts.size) > 1
    echo -> "multiple hosts"
elif $(_hosts.size) == 1
    echo -> "one host"
else
    echo -> "no hosts"
end

# --- loops -----------------------------------------------------------------
for each _h in $(_hosts)
    echo -> "probing $(_h)"
end

repeat 2 times
    echo -> "tick"
end

_count -> 0
while $(_count) < 3
    _count -> $(_count) + 1
end
echo -> "counted to $(_count)"

# --- error handling --------------------------------------------------------
try
    report -> "demo-assessment.md"
catch _err
    echo -> "report failed: $(_err)"
end

# --- module lifecycle (dispatched through the tool's binding) --------------
on demo.scan
wait for demo.scan
off demo.scan

# --- gated shell-out (requires --allow-shellout or QYVORA_ALLOW_SHELLOUT=1)
exec -> echo "shell-out executed"

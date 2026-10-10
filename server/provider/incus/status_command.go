package incus

import "fmt"

// incusInstanceStatusCommand uses CLI CSV fields backed by the API, whose values are
// neither translated nor coloured. No optional JSON parser is needed on the node.
func incusInstanceStatusCommand(name string) string {
	return fmt.Sprintf("instances=$(LC_ALL=C NO_COLOR=1 incus list %s --format csv -c ns) || exit $?; printf '%%s\\n' \"$instances\" | awk -F, -v target=%s '$1 == target { count++; state=$2; sub(/\\r$/, \"\", state) } END { if (count != 1 || state == \"\") { print \"instance status unavailable\" > \"/dev/stderr\"; exit 1 } if (state == \"RUNNING\" || state == \"STOPPED\") print state; else print \"OTHER\" }'", shellSingleQuote(name), shellSingleQuote(name))
}

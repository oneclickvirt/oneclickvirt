package lxd

import "fmt"

// lxdInstanceStatusCommand reads the machine-readable status code. The JSON
// protocol is unaffected by translated or colored `lxc info` output.
func lxdInstanceStatusCommand(name string) string {
	return fmt.Sprintf("lxc list %s --format=json | jq -er --arg name %s '[.[] | select(.name == $name) | .status_code] | if length != 1 then error(\"instance status unavailable\") elif .[0] == 103 then \"RUNNING\" elif .[0] == 102 then \"STOPPED\" else \"OTHER\" end'", shellSingleQuote(name), shellSingleQuote(name))
}

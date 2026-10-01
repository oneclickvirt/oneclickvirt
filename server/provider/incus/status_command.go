package incus

import "fmt"

// incusInstanceStatusCommand reads the machine-readable status code. The JSON
// protocol is unaffected by translated or colored `incus info` output.
func incusInstanceStatusCommand(name string) string {
	return fmt.Sprintf("incus list %s --format=json | jq -er --arg name %s '[.[] | select(.name == $name) | .status_code] | if length != 1 then error(\"instance status unavailable\") elif .[0] == 103 then \"RUNNING\" elif .[0] == 102 then \"STOPPED\" else \"OTHER\" end'", shellSingleQuote(name), shellSingleQuote(name))
}

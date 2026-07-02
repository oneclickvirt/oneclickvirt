package kubevirt

import "strings"

const kubeVirtKubeconfigPath = "/etc/rancher/k3s/k3s.yaml"

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func withKubeVirtKubeconfig(command string) string {
	return "KUBECONFIG=" + shellSingleQuote(kubeVirtKubeconfigPath) + " " + command
}

func yamlDoubleQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return "\"" + s + "\""
}

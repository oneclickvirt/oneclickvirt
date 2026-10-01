package provider

import (
	"regexp"
	"strings"
)

var runtimeContainerANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// IsRuntimeInfrastructureContainer reports containers reserved by the shell
// runtime installers. They are part of the host network, not user instances.
func IsRuntimeInfrastructureContainer(name string) bool {
	name = runtimeContainerANSI.ReplaceAllString(strings.TrimSpace(name), "")
	return strings.TrimPrefix(name, "/") == "ndpresponder"
}

// IsRuntimeInfrastructureImage keeps the NDP helper image out of guest image
// choices. Its process does not provide an OS or SSH service for instances.
func IsRuntimeInfrastructureImage(repository string) bool {
	repository = strings.ToLower(runtimeContainerANSI.ReplaceAllString(strings.TrimSpace(repository), ""))
	if index := strings.LastIndex(repository, "/"); index >= 0 {
		repository = repository[index+1:]
	}
	return repository == "ndpresponder" || repository == "oneclickvirt-ndpresponder" || strings.HasPrefix(repository, "ndpresponder_")
}

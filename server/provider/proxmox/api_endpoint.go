package proxmox

import (
	"fmt"
	"net/url"
	"strings"

	"oneclickvirt/utils"
)

// apiEndpoint builds a PVE API URL with a correctly bracketed IPv6 host.
// NodeConfig.Host is normally a bare host, but accepting URL/bracketed forms
// here keeps API-only connections working for IPv4, DNS, and IPv6 alike.
func (p *ProxmoxProvider) apiEndpoint(path string) string {
	return utils.BuildEndpointURL("https", p.config.Host, 8006, path)
}

// apiGuestEndpoint chooses the PVE resource collection for the guest type and
// escapes the dynamic node/guest path segments.  Keeping this decision in one
// place prevents container lifecycle calls from accidentally using qemu URLs.
func (p *ProxmoxProvider) apiGuestEndpoint(instanceType, vmid, suffix string) (string, error) {
	return p.apiGuestEndpointAtNode(p.nodeName(), instanceType, vmid, suffix)
}

// apiGuestEndpointAtNode builds a guest endpoint using the node returned by
// discovery.  This is required for clustered PVE recovery: the controller's
// configured/default node can differ from the node currently hosting a guest.
func (p *ProxmoxProvider) apiGuestEndpointAtNode(node, instanceType, vmid, suffix string) (string, error) {
	var resource string
	switch strings.ToLower(strings.TrimSpace(instanceType)) {
	case "vm":
		resource = "qemu"
	case "container":
		resource = "lxc"
	default:
		return "", fmt.Errorf("未知的Proxmox实例类型: %s", instanceType)
	}

	node = strings.TrimSpace(node)
	vmid = strings.TrimSpace(vmid)
	if node == "" || vmid == "" {
		return "", fmt.Errorf("PVE实例API路径缺少节点或VMID")
	}
	suffix = strings.TrimSpace(suffix)
	if suffix != "" && !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	return p.apiEndpoint(fmt.Sprintf("/api2/json/nodes/%s/%s/%s%s",
		url.PathEscape(node), resource, url.PathEscape(vmid), suffix)), nil
}

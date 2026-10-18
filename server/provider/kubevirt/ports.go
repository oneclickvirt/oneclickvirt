package kubevirt

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"
)

func stablePortProtocols(protocol string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		return []string{"TCP"}, nil
	case "udp":
		return []string{"UDP"}, nil
	case "both", "tcp/udp":
		return []string{"TCP", "UDP"}, nil
	default:
		return nil, fmt.Errorf("unsupported port protocol %q", protocol)
	}
}

// Stable VM port Services follow launcher Pod replacements. TCP/UDP sharing
// one NodePort must belong to the same Service (Kubernetes allocation rule).
func (p *KubeVirtProvider) SetupStablePortMapping(ctx context.Context, name string, host, guest int, protocol string) error {
	protocols, err := stablePortProtocols(protocol)
	if err != nil {
		return err
	}
	if name != k8sResourceName(name) || name == "" || host < 1 || host > 65535 || guest < 1 || guest > 65535 {
		return fmt.Errorf("invalid VM port mapping")
	}
	encoded, _ := json.Marshal(protocols)
	cmd := stablePortServiceCommand(name, host) + fmt.Sprintf(`
uid=$(kubectl get vm %s -n %s -o jsonpath='{.metadata.uid}')
[ -n "$uid" ]
previous=$(kubectl get svc "$svc" -n %s --ignore-not-found=true -o json)
[ -n "$previous" ] || previous='{"spec":{"ports":[]}}'
printf '%%s' "$previous" | jq --arg svc "$svc" --arg name %s --arg ns %s --arg uid "$uid" --argjson protocols %s --argjson host %d --argjson guest %d '
{apiVersion:"v1",kind:"Service",metadata:{name:$svc,namespace:$ns,labels:{"oneclickvirt.io/instance":$name},ownerReferences:[{apiVersion:"kubevirt.io/v1",kind:"VirtualMachine",name:$name,uid:$uid}]},spec:{type:"NodePort",ipFamilyPolicy:"PreferDualStack",selector:{"kubevirt.io/vm":$name},ports:([.spec.ports[] | select(.protocol as $p | $protocols | index($p) | not)] + [$protocols[] | {name:(ascii_downcase),protocol:.,port:$guest,targetPort:$guest,nodePort:$host}])}}' | kubectl apply -f -`, shellSingleQuote(name), shellSingleQuote(Namespace), shellSingleQuote(Namespace), shellSingleQuote(name), shellSingleQuote(Namespace), shellSingleQuote(string(encoded)), host, guest)
	if output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, cmd); err != nil {
		return fmt.Errorf("apply stable port Service: %w: %s", err, output)
	}
	return p.removeLegacyPortMapping(name, host, guest, protocol)
}

func stablePortServiceCommand(name string, host int) string {
	digest := sha256.Sum256([]byte(name))
	svcName := fmt.Sprintf("%.40s-%x-p%d", name, digest[:3], host)
	return fmt.Sprintf(`set -eu
exec 200>/var/lock/oneclickvirt-kubevirt-ports.lock
flock -x -w 60 200
ssh_svc=$(kubectl get svc %s -n %s --ignore-not-found=true -o json)
svc=%s
if [ -n "$ssh_svc" ] && printf '%%s' "$ssh_svc" | jq -e --argjson host %d '.spec.ports[] | select(.nodePort==$host)' >/dev/null; then svc=%s; fi`, shellSingleQuote(name+"-ssh"), shellSingleQuote(Namespace), shellSingleQuote(svcName), host, shellSingleQuote(name+"-ssh"))
}

func (p *KubeVirtProvider) removeLegacyPortMapping(name string, host, guest int, protocol string) error {
	// New clusters have no legacy rules. Inspect all available tools in one
	// round trip and avoid repeatedly persisting kube-proxy's transient rules.
	check := `set -eu
found=0
available=0
comment=` + shellSingleQuote(fmt.Sprintf("pm:%s:%d:%d", name, host, guest)) + `
if command -v nft >/dev/null 2>&1; then
 available=1
 rules=$(nft -j -a list ruleset)
 case "$rules" in *"$comment"*) found=1;; esac
fi
for tool in iptables ip6tables; do
 if command -v "$tool" >/dev/null 2>&1; then
  available=1
  for table in nat filter; do
   rules=$("$tool" -w 5 -t "$table" -S)
   case "$rules" in *"$comment"*) found=1;; esac
  done
 fi
done
[ "$available" = 1 ]
printf 'complete:%s\n' "$found"`
	output, err := p.sshClient.Execute(check)
	if err != nil {
		return fmt.Errorf("inspect legacy port rules: %w", err)
	}
	switch strings.TrimSpace(output) {
	case "complete:0":
		return nil
	case "complete:1":
	default:
		return fmt.Errorf("incomplete legacy port rules inspection")
	}
	mgr := firewall.NewManager(p.sshClient, NFTTableName, "")
	if _, err := mgr.DetectBackend(FWBackendFile); err != nil {
		return err
	}
	for _, ipv6 := range []bool{false, true} {
		if err := mgr.RemoveSingleDNATForFamily("", host, guest, protocol, fmt.Sprintf("pm:%s:%d:%d", name, host, guest), ipv6); err != nil {
			return err
		}
	}
	return mgr.SaveRules()
}

func (p *KubeVirtProvider) RemoveStablePortMapping(ctx context.Context, name string, host, guest int, protocol string) error {
	protocols, err := stablePortProtocols(protocol)
	if err != nil {
		return err
	}
	if name != k8sResourceName(name) || name == "" || host < 1 || host > 65535 || guest < 1 || guest > 65535 {
		return fmt.Errorf("invalid VM port mapping")
	}
	encoded, _ := json.Marshal(protocols)
	cmd := stablePortServiceCommand(name, host) + fmt.Sprintf(`
existing=$(kubectl get svc "$svc" -n %s --ignore-not-found=true -o json)
if [ -n "$existing" ]; then
 updated=$(printf '%%s' "$existing" | jq --argjson protocols %s '.spec.ports |= map(select(.protocol as $p | $protocols | index($p) | not))')
 if printf '%%s' "$updated" | jq -e '.spec.ports | length > 0' >/dev/null; then
   printf '%%s' "$updated" | kubectl replace -f -
 else
   kubectl delete svc "$svc" -n %s --ignore-not-found=true
 fi
fi`, shellSingleQuote(Namespace), shellSingleQuote(string(encoded)), shellSingleQuote(Namespace))
	if output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, cmd); err != nil {
		return fmt.Errorf("delete stable port Service: %w: %s", err, output)
	}
	return p.removeLegacyPortMapping(name, host, guest, protocol)
}

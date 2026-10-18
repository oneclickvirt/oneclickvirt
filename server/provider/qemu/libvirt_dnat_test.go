package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibvirtDNATHookMovesOwnedRuleBeforeReject(t *testing.T) {
	for _, backend := range []string{"iptables", "nft"} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("flock", "#!/bin/sh\nexit 0\n")
			write("iptables", `#!/bin/sh
[ "$MODE" = iptables ] || exit 1
case "$*" in
  *' -S LIBVIRT_FWI') exit 0 ;;
  *' -C LIBVIRT_FWI '*) test -f "$FIXTURE/owned" ;;
  *' -D LIBVIRT_FWI '*) rm "$FIXTURE/owned"; echo delete >> "$FIXTURE/events" ;;
  *' -I LIBVIRT_FWI 1 '*)
    echo "$*" >> "$FIXTURE/events"; touch "$FIXTURE/owned" ;;
  *) exit 2 ;;
esac
`)
			write("nft", `#!/bin/sh
[ "$MODE" = nft ] || exit 1
case "$*" in
  'list chain ip libvirt_network guest_input'|'-a list chain ip libvirt_network guest_input')
    if [ -f "$FIXTURE/owned" ]; then echo 'comment "oneclickvirt-qemu-dnat" # handle 42'; fi ;;
  'delete rule ip libvirt_network guest_input handle 42') rm "$FIXTURE/owned"; echo delete >> "$FIXTURE/events" ;;
  'insert rule ip libvirt_network guest_input '*)
    echo "$*" >> "$FIXTURE/events"; touch "$FIXTURE/owned" ;;
  *) exit 2 ;;
esac
`)
			script := strings.ReplaceAll(libvirtDNATHook, "/run/lock/oneclickvirt-qemu-dnat.lock", filepath.Join(dir, "lock"))
			script = strings.ReplaceAll(script, "/usr/local/bin/qemu_bridge", filepath.Join(dir, "bridge"))
			write("hook", script)
			// The old owned rule can be behind libvirt's reject after an update.
			if backend == "iptables" {
				write("owned", "")
			}
			for _, args := range [][]string{{"--apply"}, {"default", "updated", "begin"}} {
				cmd := exec.Command("bash", append([]string{filepath.Join(dir, "hook")}, args...)...)
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "MODE="+backend, "FIXTURE="+dir)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("hook: %v: %s", err, out)
				}
			}
			events, err := os.ReadFile(filepath.Join(dir, "events"))
			if err != nil {
				t.Fatal(err)
			}
			text := string(events)
			for _, scope := range []string{"192.168.122.0/24", "virbr0"} {
				if !strings.Contains(text, scope) {
					t.Fatalf("unscoped DNAT exception: %s", text)
				}
			}
			if backend == "iptables" && (!strings.HasPrefix(text, "delete\n") || strings.Count(text, "-I LIBVIRT_FWI 1") != 2) {
				t.Fatalf("stale owned exception not repositioned: %s", text)
			}
			if backend == "nft" && strings.Count(text, "ct status dnat") != 2 {
				t.Fatalf("nft exception missing or duplicated: %s", text)
			}
		})
	}
}

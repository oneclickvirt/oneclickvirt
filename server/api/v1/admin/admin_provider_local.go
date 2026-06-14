package admin

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

type localCommandCheck struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Present bool   `json:"present"`
}

type localRuntimeCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Output  string `json:"output,omitempty"`
	Warning string `json:"warning,omitempty"`
}

// DetectLocalProvider performs a read-only capability check for local libvirt/QEMU mode.
func DetectLocalProvider(c *gin.Context) {
	commands := map[string]localCommandCheck{
		"virsh":              lookupLocalCommand("virsh"),
		"qemu-img":           lookupLocalCommand("qemu-img"),
		"qemu-system-x86_64": lookupLocalCommand("qemu-system-x86_64"),
		"virt-install":       lookupLocalCommand("virt-install"),
		"lxc-start":          lookupLocalCommand("lxc-start"),
	}

	kvmAvailable := fileExists("/dev/kvm") || fileExists("/sys/module/kvm")
	qemuURI := runLocalCheck("libvirt-qemu", "virsh", "-c", "qemu:///system", "uri")
	lxcURI := runLocalCheck("libvirt-lxc", "virsh", "-c", "lxc:///", "uri")
	libvirtd := runSystemctlCheck("libvirtd")
	virtqemud := runSystemctlCheck("virtqemud")

	checks := map[string]localRuntimeCheck{
		"kvm": {
			Name: "kvm",
			OK:   kvmAvailable,
		},
		"libvirt-qemu": qemuURI,
		"libvirt-lxc":  lxcURI,
	}
	if libvirtd.Name != "" {
		checks["libvirtd"] = libvirtd
	}
	if virtqemud.Name != "" {
		checks["virtqemud"] = virtqemud
	}

	warnings := make([]string, 0)
	if !commands["virsh"].Present {
		warnings = append(warnings, "virsh not found; local libvirt management is unavailable")
	}
	if !commands["qemu-img"].Present {
		warnings = append(warnings, "qemu-img not found; image preparation may fail")
	}
	if !commands["virt-install"].Present {
		warnings = append(warnings, "virt-install not found; VM creation may need the fallback command path")
	}
	if !kvmAvailable {
		warnings = append(warnings, "KVM device/module not detected; QEMU may fall back to software emulation")
	}
	if !qemuURI.OK {
		warnings = append(warnings, "qemu:///system is not reachable by the controller process")
	}
	if !lxcURI.OK {
		warnings = append(warnings, "lxc:/// is not reachable; local LXC container support may be unavailable")
	}
	if runningInContainer() {
		warnings = append(warnings, "controller appears to be running in a container; host libvirt sockets/devices must be mounted")
	}

	qemuAvailable := commands["virsh"].Present && qemuURI.OK && commands["qemu-img"].Present
	lxcAvailable := commands["virsh"].Present && lxcURI.OK

	common.ResponseSuccess(c, gin.H{
		"available":     qemuAvailable || lxcAvailable,
		"kvmAvailable":  kvmAvailable,
		"qemuAvailable": qemuAvailable,
		"lxcAvailable":  lxcAvailable,
		"inContainer":   runningInContainer(),
		"os":            runtime.GOOS,
		"architecture":  runtime.GOARCH,
		"commands":      commands,
		"checks":        checks,
		"detectScript":  "scripts/local.sh",
		"installScript": "scripts/local_install.sh",
		"warnings":      warnings,
	}, "本机 Provider 检测完成")
}

func lookupLocalCommand(name string) localCommandCheck {
	path, err := exec.LookPath(name)
	return localCommandCheck{
		Name:    name,
		Path:    path,
		Present: err == nil,
	}
}

func runLocalCheck(name string, command string, args ...string) localRuntimeCheck {
	if _, err := exec.LookPath(command); err != nil {
		return localRuntimeCheck{Name: name, OK: false, Warning: command + " not found"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, command, args...).CombinedOutput()
	output := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return localRuntimeCheck{Name: name, OK: false, Output: output, Warning: "check timed out"}
	}
	if err != nil {
		return localRuntimeCheck{Name: name, OK: false, Output: output, Warning: err.Error()}
	}
	return localRuntimeCheck{Name: name, OK: true, Output: output}
}

func runSystemctlCheck(service string) localRuntimeCheck {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return localRuntimeCheck{}
	}
	check := runLocalCheck(service, "systemctl", "is-active", service)
	if check.Output == "" && !check.OK {
		check.Output = "unknown"
	}
	return check
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runningInContainer() bool {
	if fileExists("/.dockerenv") || fileExists("/run/.containerenv") {
		return true
	}
	content, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	text := strings.ToLower(string(content))
	return strings.Contains(text, "docker") ||
		strings.Contains(text, "kubepods") ||
		strings.Contains(text, "containerd") ||
		strings.Contains(text, "libpod")
}

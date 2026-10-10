package utils

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func runtimeQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

// RemoveUnusedRuntimeImage also runs after cancellation, with a bounded cleanup
// context. Never force removal: Podman can delete containers using that image.
func RemoveUnusedRuntimeImage(exec ShellExecutor, cli, image string) error {
	_, err := ExecuteShellCommandTimeoutContext(context.Background(), exec,
		fmt.Sprintf("%s rmi %s", cli, runtimeQuote(image)), 30*time.Second)
	return err
}

// RuntimeContainerState uses machine state fields and rejects ambiguous output.
func RuntimeContainerState(ctx context.Context, exec ShellExecutor, cli, id string) (string, error) {
	output, err := ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("LC_ALL=C NO_COLOR=1 %s inspect %s --format '{{.State.Status}}'", cli, runtimeQuote(id)))
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w; output: %s", id, err, TruncateString(output, 2000))
	}
	state := strings.ToLower(strings.TrimSpace(StripTerminalCSI(output)))
	switch state {
	case "running", "created", "configured", "exited", "stopped", "paused", "restarting", "removing", "dead":
		return state, nil
	default:
		return "", fmt.Errorf("unrecognized container state for %s: %q", id, TruncateString(state, 300))
	}
}

func runtimeStopped(state string) bool {
	return state == "created" || state == "configured" || state == "exited" || state == "stopped"
}

func waitRuntimeContainerState(ctx context.Context, exec ShellExecutor, cli, id string, running bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		state, err := RuntimeContainerState(waitCtx, exec, cli, id)
		if err != nil {
			return err
		}
		if (running && state == "running") || (!running && runtimeStopped(state)) {
			return nil
		}
		if err := SleepContext(waitCtx, time.Second); err != nil {
			return fmt.Errorf("container %s did not reach requested state (last state: %s): %w", id, state, err)
		}
	}
}

// EnsureRuntimeContainerRunning never restarts a running or transitioning guest.
func EnsureRuntimeContainerRunning(ctx context.Context, exec ShellExecutor, cli, id string) error {
	state, err := RuntimeContainerState(ctx, exec, cli, id)
	if err != nil {
		return err
	}
	if state == "running" {
		return nil
	}
	action := "start"
	switch {
	case state == "paused":
		action = "unpause"
	case state == "restarting":
		return waitRuntimeContainerState(ctx, exec, cli, id, true)
	case !runtimeStopped(state):
		return fmt.Errorf("cannot start container %s in state %s", id, state)
	}
	output, commandErr := ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("%s %s %s", cli, action, runtimeQuote(id)))
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if commandErr != nil {
		state, inspectErr := RuntimeContainerState(ctx, exec, cli, id)
		if inspectErr != nil || state != "running" {
			return fmt.Errorf("%s container %s: %w; output: %s; verification: %v", action, id, commandErr, TruncateString(output, 2000), inspectErr)
		}
		return nil
	}
	return waitRuntimeContainerState(ctx, exec, cli, id, true)
}

// StopRuntimeContainer reports success only after the guest is confirmed stopped.
func StopRuntimeContainer(ctx context.Context, exec ShellExecutor, cli, id string) error {
	state, err := RuntimeContainerState(ctx, exec, cli, id)
	if err != nil {
		return err
	}
	if runtimeStopped(state) {
		return nil
	}
	if state == "paused" {
		if output, err := ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("%s unpause %s", cli, runtimeQuote(id))); err != nil {
			return fmt.Errorf("unpause before stop: %w; output: %s", err, TruncateString(output, 2000))
		}
	}
	output, commandErr := ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("%s stop %s", cli, runtimeQuote(id)))
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if commandErr != nil {
		state, inspectErr := RuntimeContainerState(ctx, exec, cli, id)
		if inspectErr != nil || !runtimeStopped(state) {
			return fmt.Errorf("stop container %s: %w; output: %s; verification: %v", id, commandErr, TruncateString(output, 2000), inspectErr)
		}
		return nil
	}
	return waitRuntimeContainerState(ctx, exec, cli, id, false)
}

// RequireRuntimeContainerAbsent protects pre-existing resources on duplicate creates.
func RequireRuntimeContainerAbsent(ctx context.Context, exec ShellExecutor, cli, name string) error {
	if !IsValidContainerRuntimeName(name) {
		return fmt.Errorf("invalid container name: %q", name)
	}
	output, err := ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("LC_ALL=C NO_COLOR=1 %s ps -a --format '{{.Names}}'", cli))
	if err != nil {
		return fmt.Errorf("check target container: %w; output: %s", err, TruncateString(output, 2000))
	}
	for _, line := range strings.Split(StripTerminalCSI(output), "\n") {
		found := strings.TrimPrefix(strings.TrimSpace(line), "/")
		if found == "" {
			continue
		}
		if !IsValidContainerRuntimeName(found) {
			return fmt.Errorf("cannot verify target container: unexpected listing output")
		}
		if found == name {
			return fmt.Errorf("container %s already exists; refusing to overwrite it", name)
		}
	}
	return nil
}

// RemoveOwnedRuntimeContainer removes only the immutable ID created by this attempt.
func RemoveOwnedRuntimeContainer(ctx context.Context, exec ShellExecutor, cli, name, owner string) error {
	output, err := ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("LC_ALL=C NO_COLOR=1 %s ps -a --no-trunc --format '{{.ID}}\\t{{.Names}}'", cli))
	if err != nil {
		return fmt.Errorf("inspect failed create: %w", err)
	}
	for _, line := range strings.Split(StripTerminalCSI(output), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "/") != name {
			continue
		}
		id := fields[0]
		if !IsValidContainerRuntimeName(id) {
			return fmt.Errorf("invalid container ID during cleanup")
		}
		label, err := ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("%s inspect %s --format '{{index .Config.Labels \"oneclickvirt.create-token\"}}'", cli, runtimeQuote(id)))
		if err != nil {
			return fmt.Errorf("verify failed container ownership: %w", err)
		}
		if strings.TrimSpace(StripTerminalCSI(label)) != owner {
			return fmt.Errorf("container %s belongs to another create attempt; preserving it", name)
		}
		_, err = ExecuteShellCommandContext(ctx, exec, fmt.Sprintf("%s rm -f %s", cli, runtimeQuote(id)))
		return err
	}
	return nil
}

package utils

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// SafeShellExecutor wraps a ShellExecutor with nil-safety.
// All method calls are safe even when the inner executor is nil —
// they return descriptive errors instead of panicking.
//
// This allows providers to store SafeShellExecutor as the concrete type
// and never set it to nil, eliminating an entire class of nil pointer panics.
type SafeShellExecutor struct {
	mu       sync.RWMutex
	executor ShellExecutor
}

// NewSafeShellExecutor creates a SafeShellExecutor wrapping the given executor.
// If executor is nil, all methods return errors gracefully.
func NewSafeShellExecutor(executor ShellExecutor) *SafeShellExecutor {
	return &SafeShellExecutor{executor: executor}
}

// SetExecutor atomically replaces the inner executor.
func (s *SafeShellExecutor) SetExecutor(executor ShellExecutor) {
	s.mu.Lock()
	s.executor = executor
	s.mu.Unlock()
}

// ClearExecutor atomically clears the inner executor (sets to nil).
// After this call, all methods return errors indicating the executor is not initialized.
func (s *SafeShellExecutor) ClearExecutor() {
	s.mu.Lock()
	s.executor = nil
	s.mu.Unlock()
}

// GetExecutor returns the current inner executor (may be nil).
func (s *SafeShellExecutor) GetExecutor() ShellExecutor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.executor
}

// HasExecutor returns true if the inner executor is non-nil.
func (s *SafeShellExecutor) HasExecutor() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.executor != nil
}

func (s *SafeShellExecutor) Execute(command string) (string, error) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return "", fmt.Errorf("SSH client not initialized: provider may be disconnected")
	}
	return exec.Execute(command)
}

func (s *SafeShellExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return "", fmt.Errorf("SSH client not initialized: provider may be disconnected")
	}
	return exec.ExecuteWithTimeout(command, timeout)
}

func (s *SafeShellExecutor) ExecuteWithLogging(command string, logPrefix string) (string, error) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return "", fmt.Errorf("SSH client not initialized: provider may be disconnected")
	}
	return exec.ExecuteWithLogging(command, logPrefix)
}

func (s *SafeShellExecutor) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return "", fmt.Errorf("SSH client not initialized: provider may be disconnected")
	}
	return exec.ExecuteRaw(command, timeout)
}

func (s *SafeShellExecutor) ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return "", fmt.Errorf("SSH client not initialized: provider may be disconnected")
	}
	return exec.ExecuteViaTempScript(scriptContent, args, timeout)
}

func (s *SafeShellExecutor) UploadContent(content, remotePath string, perm os.FileMode) error {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return fmt.Errorf("SSH client not initialized: provider may be disconnected")
	}
	return exec.UploadContent(content, remotePath, perm)
}

func (s *SafeShellExecutor) IsHealthy() bool {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return false
	}
	return exec.IsHealthy()
}

func (s *SafeShellExecutor) Reconnect() error {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return fmt.Errorf("SSH client not initialized: provider may be disconnected")
	}
	return exec.Reconnect()
}

func (s *SafeShellExecutor) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executor == nil {
		return nil
	}
	err := s.executor.Close()
	s.executor = nil
	return err
}

// Ensure SafeShellExecutor implements ShellExecutor
var _ ShellExecutor = (*SafeShellExecutor)(nil)

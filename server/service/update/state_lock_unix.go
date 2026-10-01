//go:build linux || darwin

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// The HTTP process and detached worker publish through the same lock. Keep
// the lock file stable across atomic state.json replacements.
func lockOperationState(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(dir, "state.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
		}
		if err != unix.EAGAIN && err != unix.EWOULDBLOCK || time.Now().After(deadline) {
			_ = unix.Close(fd)
			return nil, fmt.Errorf("获取升级状态锁失败: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

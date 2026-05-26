package remote

import (
	"context"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

const DefaultSFTPChunkCleanupInterval = 30 * time.Minute

type sftpCleanupTargetState struct {
	target   SSHAccessTarget
	dirs     map[string]struct{}
	lastSeen time.Time
}

var (
	sftpCleanupRegistryMu sync.RWMutex
	sftpCleanupRegistry   = make(map[string]*sftpCleanupTargetState)
	sftpCleanupStartOnce  sync.Once
)

func cleanupTargetKey(target *SSHAccessTarget) string {
	if target == nil {
		return ""
	}
	return fmt.Sprintf("%d|%s|%d|%s|%t", target.ProviderID, target.Host, target.Port, target.Username, target.UseAgentTunnel)
}

func RegisterSFTPChunkCleanupTarget(target *SSHAccessTarget, remoteDir string) {
	if target == nil {
		return
	}
	dir := NormalizeRemotePath(remoteDir)
	if dir == "" {
		return
	}

	key := cleanupTargetKey(target)
	if key == "" {
		return
	}

	now := time.Now()

	sftpCleanupRegistryMu.Lock()
	defer sftpCleanupRegistryMu.Unlock()

	state, ok := sftpCleanupRegistry[key]
	if !ok {
		state = &sftpCleanupTargetState{
			target: SSHAccessTarget{
				ProviderID:     target.ProviderID,
				Host:           target.Host,
				Port:           target.Port,
				Username:       target.Username,
				Password:       target.Password,
				PrivateKey:     target.PrivateKey,
				UseAgentTunnel: target.UseAgentTunnel,
			},
			dirs: make(map[string]struct{}),
		}
		sftpCleanupRegistry[key] = state
	}

	state.lastSeen = now
	state.dirs[dir] = struct{}{}
}

func StartSFTPChunkCleanupTask(ctx context.Context, interval time.Duration, ttl time.Duration) {
	sftpCleanupStartOnce.Do(func() {
		if ctx == nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("跳过启动SFTP分片清理任务: 上下文为空")
			}
			return
		}
		if interval <= 0 {
			interval = DefaultSFTPChunkCleanupInterval
		}
		if ttl <= 0 {
			ttl = DefaultChunkPartTTL
		}

		go runSFTPChunkCleanupLoop(ctx, interval, ttl)

		if global.APP_LOG != nil {
			global.APP_LOG.Info("SFTP分片后台清理任务已启动",
				zap.Duration("interval", interval),
				zap.Duration("ttl", ttl),
			)
		}
	})
}

func runSFTPChunkCleanupLoop(ctx context.Context, interval time.Duration, ttl time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runSFTPChunkCleanupOnce(ttl)
		}
	}
}

func runSFTPChunkCleanupOnce(ttl time.Duration) {
	type cleanupSnapshot struct {
		target SSHAccessTarget
		dirs   []string
		key    string
	}

	now := time.Now()
	staleAfter := 14 * 24 * time.Hour

	sftpCleanupRegistryMu.Lock()
	snapshots := make([]cleanupSnapshot, 0, len(sftpCleanupRegistry))
	for key, state := range sftpCleanupRegistry {
		if state == nil {
			delete(sftpCleanupRegistry, key)
			continue
		}
		if now.Sub(state.lastSeen) > staleAfter {
			delete(sftpCleanupRegistry, key)
			continue
		}

		dirs := make([]string, 0, len(state.dirs))
		for dir := range state.dirs {
			dirs = append(dirs, dir)
		}
		snapshots = append(snapshots, cleanupSnapshot{target: state.target, dirs: dirs, key: key})
	}
	sftpCleanupRegistryMu.Unlock()

	for _, item := range snapshots {
		sftpClient, cleanup, err := OpenSFTPClient(&item.target)
		if err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Debug("SFTP分片后台清理连接失败",
					zap.String("target", item.key),
					zap.Error(err),
				)
			}
			continue
		}

		totalCleaned := 0
		for _, dir := range item.dirs {
			cleaned, cErr := CleanupExpiredChunkParts(sftpClient, dir, ttl)
			if cErr != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Debug("SFTP分片后台清理目录失败",
						zap.String("target", item.key),
						zap.String("dir", dir),
						zap.Error(cErr),
					)
				}
				continue
			}
			totalCleaned += cleaned
		}

		cleanup()

		if totalCleaned > 0 && global.APP_LOG != nil {
			global.APP_LOG.Info("SFTP分片后台清理完成",
				zap.String("target", item.key),
				zap.Int("cleaned_parts", totalCleaned),
			)
		}
	}
}

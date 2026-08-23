package update

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type backupManifest struct {
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	Flavor    string    `json:"flavor"`
}

func (s *Service) listBackups(cfg runtimeConfig) []Backup {
	updateDir, err := ensureUpdateDir(cfg)
	if err != nil {
		return []Backup{}
	}
	entries, err := os.ReadDir(filepath.Join(updateDir, "backups"))
	if err != nil {
		return []Backup{}
	}
	backups := make([]Backup, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !safeBackupID(entry.Name()) {
			continue
		}
		manifestPath := filepath.Join(updateDir, "backups", entry.Name(), "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var manifest backupManifest
		if json.Unmarshal(data, &manifest) != nil || strings.TrimSpace(manifest.Version) == "" {
			continue
		}
		backups = append(backups, Backup{ID: entry.Name(), Version: manifest.Version, CreatedAt: manifest.CreatedAt, Flavor: manifest.Flavor})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].CreatedAt.After(backups[j].CreatedAt) })
	return backups
}

func (s *Service) createBackup(cfg runtimeConfig, version string) (Backup, string, error) {
	updateDir, err := ensureUpdateDir(cfg)
	if err != nil {
		return Backup{}, "", err
	}
	backupRoot := filepath.Join(updateDir, "backups")
	if err := os.MkdirAll(backupRoot, 0750); err != nil {
		return Backup{}, "", err
	}
	backupID := fmt.Sprintf("%d-%s", s.now().UnixNano(), safeVersionForPath(version))
	backupDir := filepath.Join(backupRoot, backupID)
	if err := os.MkdirAll(backupDir, 0750); err != nil {
		return Backup{}, "", err
	}
	cleanup := func() {
		_ = os.RemoveAll(backupDir)
	}
	if err := copyFile(cfg.ServerPath, filepath.Join(backupDir, "server"), 0755); err != nil {
		cleanup()
		return Backup{}, "", fmt.Errorf("备份主控二进制失败: %w", err)
	}
	if cfg.UpdateWeb && directoryExists(cfg.WebPath) {
		if err := copyDir(cfg.WebPath, filepath.Join(backupDir, "web")); err != nil {
			cleanup()
			return Backup{}, "", fmt.Errorf("备份 Web 文件失败: %w", err)
		}
	}
	manifest := backupManifest{Version: version, CreatedAt: s.now(), Flavor: cfg.Flavor}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		cleanup()
		return Backup{}, "", err
	}
	if err := atomicWrite(filepath.Join(backupDir, "manifest.json"), data, 0640); err != nil {
		cleanup()
		return Backup{}, "", err
	}
	s.pruneBackups(cfg, backupID, 5)
	return Backup{ID: backupID, Version: version, CreatedAt: manifest.CreatedAt, Flavor: cfg.Flavor}, backupDir, nil
}

func (s *Service) pruneBackups(cfg runtimeConfig, keepID string, max int) {
	backups := s.listBackups(cfg)
	kept := 0
	updateDir, err := ensureUpdateDir(cfg)
	if err != nil {
		return
	}
	for _, backup := range backups {
		if backup.ID == keepID {
			kept++
			continue
		}
		if kept >= max {
			_ = os.RemoveAll(filepath.Join(updateDir, "backups", backup.ID))
			continue
		}
		kept++
	}
}

func (s *Service) backupByID(cfg runtimeConfig, id string) (Backup, string, bool) {
	if !safeBackupID(id) {
		return Backup{}, "", false
	}
	for _, backup := range s.listBackups(cfg) {
		if backup.ID != id {
			continue
		}
		updateDir, err := ensureUpdateDir(cfg)
		if err != nil {
			return Backup{}, "", false
		}
		path := filepath.Join(updateDir, "backups", id)
		if !fileExists(filepath.Join(path, "server")) {
			return Backup{}, "", false
		}
		return backup, path, true
	}
	return Backup{}, "", false
}

func safeBackupID(value string) bool {
	if value == "" || len(value) > 160 || strings.Contains(value, "..") || strings.ContainsAny(value, "/\\\x00") {
		return false
	}
	return true
}

func safeVersionForPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyDir(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("不是目录")
	}
	return filepath.Walk(source, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, entry.Mode().Perm())
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Web 目录包含不安全的符号链接: %s", relative)
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("Web 目录包含不支持的文件类型: %s", relative)
		}
		return copyFile(path, target, entry.Mode().Perm())
	})
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

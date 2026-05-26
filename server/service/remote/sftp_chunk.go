package remote

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

type ChunkUploadMeta struct {
	RemotePath  string
	UploadID    string
	ChunkIndex  int64
	TotalChunks int64
	Offset      int64
	IsLastChunk bool
}

type ChunkUploadStatus struct {
	UploadedBytes int64 `json:"uploadedBytes"`
	Completed     bool  `json:"completed"`
}

const DefaultChunkPartTTL = 72 * time.Hour

func sanitizeUploadID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	b := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b = append(b, c)
		}
	}
	if len(b) == 0 {
		return ""
	}
	return string(b)
}

func NormalizeChunkUploadID(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("upload id is required")
	}
	safeID := sanitizeUploadID(trimmed)
	if safeID == "" || safeID != trimmed {
		return "", fmt.Errorf("invalid upload id")
	}
	return safeID, nil
}

func chunkTempPath(remotePath, uploadID string) (string, error) {
	safeID, err := NormalizeChunkUploadID(uploadID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.part.%s", remotePath, safeID), nil
}

func QueryChunkUploadStatus(client *sftp.Client, remotePath, uploadID string) (ChunkUploadStatus, error) {
	if client == nil {
		return ChunkUploadStatus{}, fmt.Errorf("sftp client is nil")
	}
	if strings.TrimSpace(remotePath) == "" {
		return ChunkUploadStatus{}, fmt.Errorf("remote path is required")
	}
	if strings.TrimSpace(uploadID) == "" {
		return ChunkUploadStatus{}, fmt.Errorf("upload id is required")
	}

	tmpPath, err := chunkTempPath(remotePath, uploadID)
	if err != nil {
		return ChunkUploadStatus{}, err
	}
	if info, err := client.Stat(tmpPath); err == nil {
		return ChunkUploadStatus{UploadedBytes: info.Size(), Completed: false}, nil
	}

	if info, err := client.Stat(remotePath); err == nil {
		return ChunkUploadStatus{UploadedBytes: info.Size(), Completed: true}, nil
	}

	return ChunkUploadStatus{UploadedBytes: 0, Completed: false}, nil
}

func AbortChunkUpload(client *sftp.Client, remotePath, uploadID string) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("sftp client is nil")
	}
	if strings.TrimSpace(remotePath) == "" {
		return false, fmt.Errorf("remote path is required")
	}
	if strings.TrimSpace(uploadID) == "" {
		return false, fmt.Errorf("upload id is required")
	}

	tmpPath, err := chunkTempPath(remotePath, uploadID)
	if err != nil {
		return false, err
	}
	if err := client.Remove(tmpPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func CleanupExpiredChunkParts(client *sftp.Client, remoteDir string, ttl time.Duration) (int, error) {
	if client == nil {
		return 0, fmt.Errorf("sftp client is nil")
	}
	remoteDir = strings.TrimSpace(remoteDir)
	if remoteDir == "" {
		return 0, fmt.Errorf("remote dir is required")
	}
	if ttl <= 0 {
		ttl = DefaultChunkPartTTL
	}

	entries, err := client.ReadDir(remoteDir)
	if err != nil {
		return 0, err
	}

	now := time.Now()
	cleaned := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.Contains(name, ".part.") {
			continue
		}
		if now.Sub(entry.ModTime()) < ttl {
			continue
		}
		if err := client.Remove(path.Join(remoteDir, name)); err != nil {
			continue
		}
		cleaned++
	}

	return cleaned, nil
}

// WriteSFTPChunk writes one chunk into a remote temp file and optionally commits it.
// It supports offset writes so callers can resume from disconnections.
func WriteSFTPChunk(client *sftp.Client, meta ChunkUploadMeta, chunk io.Reader) (written int64, committed bool, err error) {
	if client == nil {
		return 0, false, fmt.Errorf("sftp client is nil")
	}
	if strings.TrimSpace(meta.RemotePath) == "" {
		return 0, false, fmt.Errorf("remote path is required")
	}
	if strings.TrimSpace(meta.UploadID) == "" {
		return 0, false, fmt.Errorf("upload id is required")
	}
	if meta.Offset < 0 {
		return 0, false, fmt.Errorf("invalid chunk offset")
	}
	if meta.ChunkIndex < 0 {
		return 0, false, fmt.Errorf("invalid chunk index")
	}
	if meta.TotalChunks <= 0 {
		return 0, false, fmt.Errorf("invalid total chunks")
	}

	tmpPath, err := chunkTempPath(meta.RemotePath, meta.UploadID)
	if err != nil {
		return 0, false, err
	}
	remoteDir := path.Dir(meta.RemotePath)
	if remoteDir == "" {
		remoteDir = "/"
	}
	if err := client.MkdirAll(remoteDir); err != nil {
		return 0, false, fmt.Errorf("create remote dir failed: %w", err)
	}

	fd, err := client.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return 0, false, fmt.Errorf("open temp file failed: %w", err)
	}
	defer fd.Close()

	buf := make([]byte, 128*1024)
	offset := meta.Offset
	for {
		n, rErr := chunk.Read(buf)
		if n > 0 {
			if _, wErr := fd.WriteAt(buf[:n], offset); wErr != nil {
				return written, false, fmt.Errorf("write chunk failed: %w", wErr)
			}
			offset += int64(n)
			written += int64(n)
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			return written, false, fmt.Errorf("read chunk failed: %w", rErr)
		}
	}

	if !meta.IsLastChunk {
		return written, false, nil
	}

	_ = fd.Close()
	_ = client.Remove(meta.RemotePath)
	if err := client.Rename(tmpPath, meta.RemotePath); err != nil {
		return written, false, fmt.Errorf("commit uploaded file failed: %w", err)
	}

	return written, true, nil
}

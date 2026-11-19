package remote

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/sync/semaphore"
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

const (
	DefaultChunkPartTTL = 72 * time.Hour
	chunkFilePrefix     = ".oneclickvirt-upload-"
)

var (
	ErrChunkSequence = errors.New("upload chunk does not match the next expected index or offset")
	chunkFilePattern = regexp.MustCompile(`^(\.oneclickvirt-upload-[0-9a-f]{64})(?:-[0-9a-f]{64}\.(?:part|json)|\.commit)(?:\.(?:new|old))?$`)
	chunkLocks       = func() [256]*semaphore.Weighted {
		var locks [256]*semaphore.Weighted
		for i := range locks {
			locks[i] = semaphore.NewWeighted(1)
		}
		return locks
	}()
)

// IsSFTPChunkFile identifies the metadata and staging files reserved by the uploader.
func IsSFTPChunkFile(name string) bool {
	return chunkFilePattern.MatchString(name)
}

func NormalizeChunkUploadID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", errors.New("upload id is required")
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", errors.New("invalid upload id")
		}
	}
	return id, nil
}

type chunkFiles struct {
	namespace   string
	payload     string
	progress    string
	receipt     string
	fingerprint string
}

func getChunkFiles(remotePath, uploadID string) (chunkFiles, error) {
	id, err := NormalizeChunkUploadID(uploadID)
	if err != nil {
		return chunkFiles{}, err
	}
	name := path.Base(remotePath)
	if strings.TrimSpace(remotePath) == "" || name == "/" || name == "." || name == ".." || IsSFTPChunkFile(name) {
		return chunkFiles{}, errors.New("invalid upload destination")
	}
	namespace := path.Join(path.Dir(remotePath), fmt.Sprintf("%s%x", chunkFilePrefix, sha256.Sum256([]byte(name))))
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
	base := namespace + "-" + fingerprint
	return chunkFiles{
		namespace: namespace, payload: base + ".part", progress: base + ".json",
		receipt: namespace + ".commit", fingerprint: fingerprint,
	}, nil
}

func chunkLock(target *SSHAccessTarget, namespace string) *semaphore.Weighted {
	key := sha256.Sum256([]byte(cleanupTargetKey(target) + "\x00" + namespace))
	return chunkLocks[key[0]]
}

type chunkProgress struct {
	TotalChunks int64 `json:"totalChunks"`
	NextChunk   int64 `json:"nextChunk"`
	Bytes       int64 `json:"bytes"`
	LastOffset  int64 `json:"lastOffset"`
	ModTime     int64 `json:"modTime"`
}

func readChunkJSON(client *sftp.Client, name string, doc any) (bool, error) {
	file, err := client.Open(name)
	if os.IsNotExist(err) {
		file, err = client.Open(name + ".old")
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return false, err
	}
	if len(data) > 4096 {
		return false, errors.New("invalid upload metadata")
	}
	return true, json.Unmarshal(data, doc)
}

func replaceChunkFile(client *sftp.Client, from, to, backup string) error {
	if _, available := client.HasExtension("posix-rename@openssh.com"); available {
		return client.PosixRename(from, to)
	}
	if err := client.Rename(from, to); err != nil {
		var status *sftp.StatusError
		if !errors.As(err, &status) || status.FxCode() != sftp.ErrSSHFxFailure {
			return err
		}
		if existing, statErr := chunkFileInfo(client, to); statErr != nil || existing == nil {
			return errors.Join(err, statErr)
		}
		if staged, statErr := chunkFileInfo(client, from); statErr != nil || staged == nil {
			return errors.Join(err, statErr)
		}
		// Base SFTP rename may refuse to replace an existing file. Keep the
		// previous contents in the same directory until publication succeeds.
		if removeErr := client.Remove(backup); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		if backupErr := client.Rename(to, backup); backupErr != nil {
			return backupErr
		}
		if publishErr := client.Rename(from, to); publishErr != nil {
			return errors.Join(publishErr, client.Rename(backup, to))
		}
	}
	_ = client.Remove(backup)
	return nil
}

func writeChunkJSON(client *sftp.Client, name string, doc any) error {
	staged := name + ".new"
	file, err := client.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	writeErr := json.NewEncoder(file).Encode(doc)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return replaceChunkFile(client, staged, name, name+".old")
}

func chunkFileInfo(client *sftp.Client, name string) (os.FileInfo, error) {
	info, err := client.Lstat(name)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("expected a regular upload file at %s", name)
	}
	return info, nil
}

// loadChunkProgress trusts only a successfully saved checkpoint. A missing or
// incomplete staged file cannot contribute bytes to a resumed upload.
func loadChunkProgress(client *sftp.Client, files chunkFiles, remotePath string) (chunkProgress, bool, error) {
	var state chunkProgress
	found, err := readChunkJSON(client, files.progress, &state)
	if err != nil || !found {
		return chunkProgress{}, false, err
	}
	if state.TotalChunks <= 0 || state.NextChunk <= 0 || state.NextChunk > state.TotalChunks ||
		state.LastOffset < 0 || state.Bytes < state.LastOffset {
		return chunkProgress{}, false, errors.New("invalid upload checkpoint")
	}

	staged, err := chunkFileInfo(client, files.payload)
	if err != nil {
		return chunkProgress{}, false, err
	}
	if staged != nil && staged.Size() >= state.Bytes &&
		(state.NextChunk < state.TotalChunks || staged.Size() == state.Bytes) {
		return state, false, nil
	}
	if staged != nil || state.NextChunk != state.TotalChunks {
		return chunkProgress{}, false, nil
	}

	var current string
	found, err = readChunkJSON(client, files.receipt, &current)
	if err != nil || !found || current != files.fingerprint {
		return chunkProgress{}, false, err
	}
	published, err := chunkFileInfo(client, remotePath)
	if err != nil {
		return chunkProgress{}, false, err
	}
	if published == nil || published.Size() != state.Bytes || published.ModTime().Unix() != state.ModTime {
		return chunkProgress{}, false, nil
	}
	return state, true, nil
}

func QueryChunkUploadStatus(ctx context.Context, client *sftp.Client, target *SSHAccessTarget, remotePath, uploadID string) (ChunkUploadStatus, error) {
	files, err := getChunkFiles(remotePath, uploadID)
	if err != nil {
		return ChunkUploadStatus{}, err
	}
	lock := chunkLock(target, files.namespace)
	if err := lock.Acquire(ctx, 1); err != nil {
		return ChunkUploadStatus{}, err
	}
	defer lock.Release(1)

	state, committed, err := loadChunkProgress(client, files, remotePath)
	if err != nil {
		return ChunkUploadStatus{}, err
	}
	uploaded := state.Bytes
	if !committed && state.TotalChunks > 0 && state.NextChunk == state.TotalChunks {
		// The part is fully staged. Resume at the final chunk to retry publication.
		uploaded = state.LastOffset
	}
	return ChunkUploadStatus{UploadedBytes: uploaded, Completed: committed}, nil
}

func AbortChunkUpload(ctx context.Context, client *sftp.Client, target *SSHAccessTarget, remotePath, uploadID string) (bool, error) {
	files, err := getChunkFiles(remotePath, uploadID)
	if err != nil {
		return false, err
	}
	lock := chunkLock(target, files.namespace)
	if err := lock.Acquire(ctx, 1); err != nil {
		return false, err
	}
	defer lock.Release(1)

	// A connection may have dropped between the two base SFTP renames.
	backup := files.payload + ".old"
	if info, err := chunkFileInfo(client, backup); err != nil {
		return false, err
	} else if info != nil {
		if _, err := client.Lstat(remotePath); os.IsNotExist(err) {
			if err := client.Rename(backup, remotePath); err != nil {
				return false, err
			}
		} else if err != nil {
			return false, err
		} else if err := client.Remove(backup); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	if err := client.Remove(files.payload); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, name := range []string{files.progress, files.progress + ".new", files.progress + ".old"} {
		if err := client.Remove(name); err != nil && !os.IsNotExist(err) {
			return true, err
		}
	}
	return true, nil
}

func CleanupExpiredChunkParts(client *sftp.Client, target *SSHAccessTarget, remoteDir string, ttl time.Duration) (int, error) {
	if ttl <= 0 {
		ttl = DefaultChunkPartTTL
	}
	entries, err := client.ReadDir(remoteDir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-ttl)
	cleaned := 0
	for _, entry := range entries {
		// Keep a prior destination if an interrupted base SFTP rename may
		// have left it as the only copy. A retry or abort handles recovery.
		if !entry.Mode().IsRegular() || !entry.ModTime().Before(cutoff) || strings.HasSuffix(entry.Name(), ".part.old") {
			continue
		}
		match := chunkFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		lock := chunkLock(target, path.Join(remoteDir, match[1]))
		if !lock.TryAcquire(1) {
			continue
		}
		name := path.Join(remoteDir, entry.Name())
		current, err := chunkFileInfo(client, name)
		if err == nil && current != nil && current.ModTime().Before(cutoff) && client.Remove(name) == nil {
			cleaned++
		}
		lock.Release(1)
	}
	return cleaned, nil
}

// WriteSFTPChunk advances a contiguous checkpoint and installs the complete
// staged file. Replays of accepted chunks do not rewrite the file, and the
// previous destination is retained until its replacement is installed.
func WriteSFTPChunk(ctx context.Context, client *sftp.Client, target *SSHAccessTarget, meta ChunkUploadMeta, chunk io.Reader) (int64, bool, error) {
	if meta.TotalChunks <= 0 || meta.ChunkIndex < 0 || meta.ChunkIndex >= meta.TotalChunks ||
		meta.Offset < 0 || (meta.ChunkIndex == 0 && meta.Offset != 0) ||
		meta.IsLastChunk != (meta.ChunkIndex == meta.TotalChunks-1) {
		return 0, false, ErrChunkSequence
	}
	files, err := getChunkFiles(meta.RemotePath, meta.UploadID)
	if err != nil {
		return 0, false, err
	}
	lock := chunkLock(target, files.namespace)
	if err := lock.Acquire(ctx, 1); err != nil {
		return 0, false, err
	}
	defer lock.Release(1)

	state, committed, err := loadChunkProgress(client, files, meta.RemotePath)
	if err != nil {
		return 0, false, err
	}
	if state.TotalChunks != 0 && state.TotalChunks != meta.TotalChunks {
		return 0, false, ErrChunkSequence
	}
	if meta.ChunkIndex < state.NextChunk {
		if meta.IsLastChunk {
			if meta.Offset != state.LastOffset {
				return 0, false, ErrChunkSequence
			}
			if !committed {
				if err := publishChunkFile(client, files, meta.RemotePath); err != nil {
					return 0, false, err
				}
			}
			return state.Bytes - state.LastOffset, true, nil
		}
		written, err := io.Copy(io.Discard, chunk)
		if meta.Offset > state.Bytes || written > state.Bytes-meta.Offset {
			return 0, false, ErrChunkSequence
		}
		return written, committed, err
	}
	if meta.ChunkIndex != state.NextChunk || meta.Offset != state.Bytes {
		return 0, false, ErrChunkSequence
	}

	if err := client.MkdirAll(path.Dir(meta.RemotePath)); err != nil {
		return 0, false, err
	}
	written, info, err := stageChunk(client, files.payload, meta.Offset, chunk)
	if err != nil {
		return written, false, err
	}
	state = chunkProgress{
		TotalChunks: meta.TotalChunks, NextChunk: meta.ChunkIndex + 1,
		Bytes: info.Size(), LastOffset: meta.Offset, ModTime: info.ModTime().Unix(),
	}
	if err := writeChunkJSON(client, files.progress, state); err != nil {
		return written, false, err
	}
	if !meta.IsLastChunk {
		return written, false, nil
	}
	if err := publishChunkFile(client, files, meta.RemotePath); err != nil {
		return written, false, err
	}
	return written, true, nil
}

func stageChunk(client *sftp.Client, name string, offset int64, chunk io.Reader) (int64, os.FileInfo, error) {
	info, err := chunkFileInfo(client, name)
	if err != nil {
		return 0, nil, err
	}
	if (info == nil && offset != 0) || (info != nil && info.Size() < offset) {
		return 0, nil, ErrChunkSequence
	}
	file, err := client.OpenFile(name, os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return 0, nil, err
	}
	// Discard any uncheckpointed tail left by a disconnected request.
	if info != nil && info.Size() > offset {
		if err := file.Truncate(offset); err != nil {
			_ = file.Close()
			return 0, nil, err
		}
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return 0, nil, err
	}
	// Use Write so a reader failure is propagated instead of being normalized
	// by the SFTP File.ReadFrom implementation.
	written, copyErr := io.CopyBuffer(struct{ io.Writer }{file}, chunk, make([]byte, 128*1024))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return written, nil, err
	}
	info, err = chunkFileInfo(client, name)
	if err != nil {
		return written, nil, err
	}
	if info == nil || info.Size()-offset != written {
		return written, nil, io.ErrShortWrite
	}
	return written, info, nil
}

func publishChunkFile(client *sftp.Client, files chunkFiles, remotePath string) error {
	// Save the identity first so a lost acknowledgment of Rename can be
	// recognized by the missing part and the matching published file.
	if err := writeChunkJSON(client, files.receipt, files.fingerprint); err != nil {
		return err
	}
	return replaceChunkFile(client, files.payload, remotePath, files.payload+".old")
}

// WriteSFTPFile keeps the legacy single-request upload usable on SFTP servers
// without atomic overwrite support. A direct write invalidates any receipt
// for the destination before opening it for writing.
func WriteSFTPFile(ctx context.Context, client *sftp.Client, target *SSHAccessTarget, remotePath string, src io.Reader) error {
	files, err := getChunkFiles(remotePath, "direct")
	if err != nil {
		return err
	}
	lock := chunkLock(target, files.namespace)
	if err := lock.Acquire(ctx, 1); err != nil {
		return err
	}
	defer lock.Release(1)

	if err := client.MkdirAll(path.Dir(remotePath)); err != nil {
		return err
	}
	if err := client.Remove(files.receipt); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := client.Remove(files.receipt + ".old"); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyBuffer(struct{ io.Writer }{file}, src, make([]byte, 128*1024))
	return errors.Join(copyErr, file.Close())
}

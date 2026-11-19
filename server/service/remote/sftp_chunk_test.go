package remote

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
)

func testChunkClient(t *testing.T, handlers *sftp.Handlers) *sftp.Client {
	t.Helper()
	conn, peer := net.Pipe()
	var server interface{ Serve() error }
	if handlers == nil {
		var err error
		server, err = sftp.NewServer(peer)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		server = sftp.NewRequestServer(peer, *handlers)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve()
	}()
	client, err := sftp.NewClientPipe(conn, conn)
	if err != nil {
		_ = conn.Close()
		_ = peer.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
		<-done
	})
	return client
}

func TestChunkedUploadRequiresContiguousChunksAndReplaysSafely(t *testing.T) {
	client := testChunkClient(t, nil)
	ctx := context.Background()
	destination := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(destination, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	chunk := ChunkUploadMeta{RemotePath: destination, UploadID: "first-upload", TotalChunks: 3}
	write := func(index, offset int64, body string, last bool) (int64, bool, error) {
		meta := chunk
		meta.ChunkIndex, meta.Offset, meta.IsLastChunk = index, offset, last
		return WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader(body))
	}
	expectFile := func(want string) {
		t.Helper()
		data, err := os.ReadFile(destination)
		if err != nil || string(data) != want {
			t.Fatalf("destination = %q, %v; want %q", data, err, want)
		}
	}
	expectStatus := func(bytes int64, completed bool) {
		t.Helper()
		got, err := QueryChunkUploadStatus(ctx, client, nil, destination, chunk.UploadID)
		if err != nil || got != (ChunkUploadStatus{UploadedBytes: bytes, Completed: completed}) {
			t.Fatalf("upload status = %+v, %v; want %d bytes, complete %t", got, err, bytes, completed)
		}
	}

	expectStatus(0, false)
	if n, done, err := write(0, 0, "abc", false); err != nil || n != 3 || done {
		t.Fatalf("first upload = %d, %t, %v", n, done, err)
	}
	if n, done, err := write(0, 0, "abc", false); err != nil || n != 3 || done {
		t.Fatalf("first replay = %d, %t, %v", n, done, err)
	}
	for _, invalid := range []struct {
		index  int64
		offset int64
		last   bool
	}{
		// A misleading contiguous offset cannot skip a chunk index.
		{2, 3, true},
		{2, 6, true},
		{1, 6, false},
		{1, 3, true},
		{3, 9, true},
	} {
		if _, _, err := write(invalid.index, invalid.offset, "bad", invalid.last); !errors.Is(err, ErrChunkSequence) {
			t.Fatalf("unexpected result for %+v: %v", invalid, err)
		}
	}
	expectFile("previous")

	// Reconnect to check that progress and retries do not depend on a session.
	client = testChunkClient(t, nil)
	expectStatus(3, false)
	if n, done, err := write(1, 3, "def", false); err != nil || n != 3 || done {
		t.Fatalf("middle upload = %d, %t, %v", n, done, err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if n, done, err := write(2, 6, "ghi", true); err != nil || n != 3 || !done {
				t.Errorf("final upload or replay = %d, %t, %v", n, done, err)
			}
		})
	}
	wg.Wait()
	expectFile("abcdefghi")
	expectStatus(9, true)

	// Even an equally sized replacement has a separate upload identity.
	chunk.UploadID, chunk.TotalChunks = "new-upload", 1
	expectStatus(0, false)
	if _, done, err := write(0, 0, "123456789", true); err != nil || !done {
		t.Fatalf("replacement upload = %t, %v", done, err)
	}
	expectFile("123456789")
	expectStatus(9, true)
	chunk.UploadID, chunk.TotalChunks = "first-upload", 3
	expectStatus(0, false)
	if _, _, err := write(2, 6, "ghi", true); !errors.Is(err, ErrChunkSequence) {
		t.Fatalf("unexpected result for superseded final chunk: %v", err)
	}
	expectFile("123456789")
}

type brokenChunkReader struct {
	data io.Reader
}

func (r brokenChunkReader) Read(p []byte) (int, error) {
	n, err := r.data.Read(p)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func TestChunkUploadRestartsFromCheckpointAfterInterruptedWrite(t *testing.T) {
	client := testChunkClient(t, nil)
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "resume")
	meta := ChunkUploadMeta{RemotePath: dest, UploadID: "resume-id", TotalChunks: 2}
	if _, _, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader("first+")); err != nil {
		t.Fatal(err)
	}
	meta.ChunkIndex, meta.Offset, meta.IsLastChunk = 1, 6, true
	interrupted := brokenChunkReader{data: strings.NewReader("uncheckpointed bytes")}
	if _, _, err := WriteSFTPChunk(ctx, client, nil, meta, interrupted); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("interrupted upload: %v", err)
	}
	status, err := QueryChunkUploadStatus(ctx, client, nil, dest, meta.UploadID)
	if err != nil || status.UploadedBytes != 6 || status.Completed {
		t.Fatalf("after interruption: %+v, %v", status, err)
	}
	if _, done, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader("final")); err != nil || !done {
		t.Fatalf("resume upload = %t, %v", done, err)
	}
	if result, err := os.ReadFile(dest); err != nil || string(result) != "first+final" {
		t.Fatalf("resumed file = %q, %v", result, err)
	}
	if removed, err := AbortChunkUpload(ctx, client, nil, dest, meta.UploadID); err != nil || removed {
		t.Fatalf("abort after commit = %t, %v", removed, err)
	}
	if err := WriteSFTPFile(ctx, client, nil, dest, strings.NewReader("replacement")); err != nil {
		t.Fatal(err)
	}
	status, err = QueryChunkUploadStatus(ctx, client, nil, dest, meta.UploadID)
	if err != nil || status.Completed || status.UploadedBytes != 0 {
		t.Fatalf("direct replacement incorrectly used old receipt: %+v, %v", status, err)
	}
}

func TestChunkUploadRetriesPublicationAndAllowsEmptyFile(t *testing.T) {
	client := testChunkClient(t, nil)
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	meta := ChunkUploadMeta{RemotePath: dest, UploadID: "empty-id", TotalChunks: 1, IsLastChunk: true}
	if _, done, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader("")); err == nil || done {
		t.Fatalf("publication over directory = %t, %v", done, err)
	}
	status, err := QueryChunkUploadStatus(ctx, client, nil, dest, meta.UploadID)
	if err != nil || status.Completed || status.UploadedBytes != 0 {
		t.Fatalf("staged file reported as complete: %+v, %v", status, err)
	}
	if info, err := os.Stat(dest); err != nil || !info.IsDir() {
		t.Fatalf("prior destination was altered: %v, %v", info, err)
	}
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if n, done, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader("")); err != nil || !done || n != 0 {
		t.Fatalf("empty-file retry = %d, %t, %v", n, done, err)
	}
	status, err = QueryChunkUploadStatus(ctx, client, nil, dest, meta.UploadID)
	if err != nil || !status.Completed || status.UploadedBytes != 0 {
		t.Fatalf("empty file upload status = %+v, %v", status, err)
	}
}

func TestChunkUploadWithoutPosixRename(t *testing.T) {
	if err := sftp.SetSFTPExtensions(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sftp.SetSFTPExtensions("hardlink@openssh.com", "posix-rename@openssh.com", "statvfs@openssh.com"); err != nil {
			t.Error(err)
		}
	})
	handlers := sftp.InMemHandler()
	client := testChunkClient(t, &handlers)
	ctx := context.Background()
	destination := "/file"
	if err := WriteSFTPFile(ctx, client, nil, destination, strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	meta := ChunkUploadMeta{RemotePath: destination, UploadID: "standard-rename", TotalChunks: 3}
	if _, done, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader("new")); err != nil || done {
		t.Fatalf("first chunk = %t, %v", done, err)
	}
	files, err := getChunkFiles(destination, meta.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a disconnect after the checkpoint was moved out of the way.
	if err := client.Rename(files.progress, files.progress+".old"); err != nil {
		t.Fatal(err)
	}
	status, err := QueryChunkUploadStatus(ctx, client, nil, destination, meta.UploadID)
	if err != nil || status.UploadedBytes != 3 || status.Completed {
		t.Fatalf("recover checkpoint: %+v, %v", status, err)
	}
	meta.ChunkIndex, meta.Offset = 1, 3
	if _, done, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader(" ")); err != nil || done {
		t.Fatalf("middle chunk = %t, %v", done, err)
	}
	meta.ChunkIndex, meta.Offset, meta.IsLastChunk = 2, 4, true
	for range 2 {
		if _, done, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader("file")); err != nil || !done {
			t.Fatalf("publish or replay = %t, %v", done, err)
		}
	}
	file, err := client.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(file)
	if err := errors.Join(err, file.Close()); err != nil || string(contents) != "new file" {
		t.Fatalf("published file = %q, %v", contents, err)
	}

	meta = ChunkUploadMeta{RemotePath: destination, UploadID: "interrupted-replace", TotalChunks: 2}
	if _, _, err := WriteSFTPChunk(ctx, client, nil, meta, strings.NewReader("discard")); err != nil {
		t.Fatal(err)
	}
	files, err = getChunkFiles(destination, meta.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a disconnect after moving the old destination to its backup.
	if err := client.Rename(destination, files.payload+".old"); err != nil {
		t.Fatal(err)
	}
	if aborted, err := AbortChunkUpload(ctx, client, nil, destination, meta.UploadID); err != nil || !aborted {
		t.Fatalf("abort after interrupted replacement = %t, %v", aborted, err)
	}
	file, err = client.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	contents, err = io.ReadAll(file)
	if err := errors.Join(err, file.Close()); err != nil || string(contents) != "new file" {
		t.Fatalf("restored file = %q, %v", contents, err)
	}
}

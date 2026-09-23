package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestStageAgentArchiveUsesBoundedVerifiedChunks(t *testing.T) {
	content := bytes.Repeat([]byte("current Agent binary\x00"), 100000)
	var uploaded []byte
	verified := false
	execute := func(ctx context.Context, command string) (string, error) {
		if len(command) > 40*1024 {
			t.Fatalf("command exceeds safe argv size: %d", len(command))
		}
		if strings.Contains(command, "base64 -d >>") {
			encoded := strings.Split(command, "'")[3]
			chunk, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			uploaded = append(uploaded, chunk...)
		}
		if strings.Contains(command, "sha256sum -c -") {
			verified = strings.Contains(command, fmt.Sprintf("%x", sha256.Sum256(uploaded)))
		}
		return "", nil
	}
	path, err := stageAgentArchive(context.Background(), execute, content)
	if err != nil || path == "" || !verified || !bytes.Equal(uploaded, content) {
		t.Fatalf("path=%q err=%v verified=%v uploaded=%d want=%d", path, err, verified, len(uploaded), len(content))
	}
}

func TestStageAgentArchiveCleansUpAfterTransferFailure(t *testing.T) {
	for _, failAt := range []string{"base64 -d", "sha256sum"} {
		t.Run(failAt, func(t *testing.T) {
			cleaned := false
			execute := func(ctx context.Context, command string) (string, error) {
				if strings.HasPrefix(command, "rm -f -- '/tmp/ocv_agent_archive_") {
					cleaned = true
				}
				if strings.Contains(command, failAt) {
					return "", errors.New("transfer failure")
				}
				return "", nil
			}
			path, err := stageAgentArchive(context.Background(), execute, []byte("test"))
			if err == nil || path != "" || !cleaned {
				t.Fatalf("path=%q err=%v cleaned=%v", path, err, cleaned)
			}
		})
	}
}

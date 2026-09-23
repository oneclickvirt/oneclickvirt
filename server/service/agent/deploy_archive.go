package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"oneclickvirt/utils"
)

type agentDeployExecutor func(context.Context, string) (string, error)

func newAgentTransferID() string { return uuid.NewString() }

// stageAgentArchive keeps each command below Linux's per-argument limit, even
// when ExecuteSSHCommand wraps it in another shell. It works for every existing
// provider transport and preserves the exact embedded artifact instead of
// silently downloading an older release when the archive exceeds 512 KiB.
func stageAgentArchive(ctx context.Context, execute agentDeployExecutor, content []byte) (string, error) {
	remotePath := "/tmp/ocv_agent_archive_" + newAgentTransferID() + ".tar.gz"
	quoted := utils.ShellSingleQuote(remotePath)
	if _, err := execute(ctx, "umask 077; set -C; : > "+quoted); err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if !complete {
			removeStagedAgentArchive(execute, remotePath)
		}
	}()
	const chunkSize = 24 * 1024
	for offset := 0; offset < len(content); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := min(offset+chunkSize, len(content))
		chunk := base64.StdEncoding.EncodeToString(content[offset:end])
		if _, err := execute(ctx, "printf '%s' '"+chunk+"' | base64 -d >> "+quoted); err != nil {
			return "", fmt.Errorf("upload Agent archive chunk: %w", err)
		}
	}
	checksumLine := fmt.Sprintf("%x  %s\n", sha256.Sum256(content), remotePath)
	if _, err := execute(ctx, "printf '%s' "+utils.ShellSingleQuote(checksumLine)+" | sha256sum -c -"); err != nil {
		return "", fmt.Errorf("verify Agent archive checksum: %w", err)
	}
	complete = true
	return remotePath, nil
}

func removeStagedAgentArchive(execute agentDeployExecutor, remotePath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = execute(ctx, "rm -f -- "+utils.ShellSingleQuote(remotePath))
}

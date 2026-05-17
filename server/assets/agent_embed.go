package assets

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

// AgentAssets contains controller-local distributable assets for agent installation.
//
//go:embed agent/*
var AgentAssets embed.FS

func ReadAgentAsset(name string) ([]byte, error) {
	clean := strings.TrimSpace(name)
	if clean == "" || strings.Contains(clean, "..") || strings.Contains(clean, "/") || strings.Contains(clean, "\\") {
		return nil, fmt.Errorf("invalid asset name: %q", name)
	}
	return AgentAssets.ReadFile(path.Join("agent", clean))
}

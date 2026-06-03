// Package mcp provides the CLI subcommand for running the MCP server
// Usage: ./oneclickvirt mcp [--token TOKEN] [--api-url URL]
package mcp

import (
	"flag"
	"fmt"
	"os"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// Run executes the MCP subcommand
func Run(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	defaultAPIURL := os.Getenv("ONE_CLICK_VIRT_API_URL")
	if defaultAPIURL == "" {
		defaultAPIURL = "http://localhost:8888"
	}
	defaultAPIToken := os.Getenv("ONE_CLICK_VIRT_API_TOKEN")

	apiURL := fs.String("api-url", defaultAPIURL, "OneClickVirt API base URL")
	apiToken := fs.String("token", defaultAPIToken, "API Bearer token (optional if server has no auth)")
	help := fs.Bool("help", false, "Show help")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *help {
		fmt.Print(`OneClickVirt MCP Server

Usage:
  oneclickvirt mcp [flags]

Flags:
  --api-url    OneClickVirt API base URL (default: http://localhost:8888)
  --token      API Bearer token for authentication
  --help       Show this help

Description:
  Starts a Model Context Protocol (MCP) server that allows AI assistants
  (Claude Desktop, GitHub Copilot, Cursor, etc.) to manage OneClickVirt
  virtualization resources through standardized tool calls.

  The MCP server communicates via stdin/stdout (JSON-RPC 2.0), which is
  the standard transport for local MCP integrations.

Configuration (Claude Desktop):
  Add the following to your claude_desktop_config.json:

  {
    "mcpServers": {
      "oneclickvirt": {
        "command": "/opt/oneclickvirt/server/oneclickvirt",
        "args": ["mcp"],
        "env": {
          "ONE_CLICK_VIRT_API_URL": "http://localhost:8888",
          "ONE_CLICK_VIRT_API_TOKEN": "YOUR_API_TOKEN"
        }
      }
    }
  }

Configuration (GitHub Copilot / VS Code):
  Add the following to .vscode/mcp.json:

  {
    "servers": {
      "oneclickvirt": {
        "type": "stdio",
        "command": "/opt/oneclickvirt/server/oneclickvirt",
        "args": ["mcp"],
        "env": {
          "ONE_CLICK_VIRT_API_URL": "http://localhost:8888",
          "ONE_CLICK_VIRT_API_TOKEN": "YOUR_API_TOKEN"
        }
      }
    }
  }
`)
		return nil
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Info("Starting OneClickVirt MCP server (stdio mode)",
			zap.String("apiURL", *apiURL),
		)
	}

	server := NewMCPServer(*apiURL, *apiToken)

	// Write a notice to stderr so it doesn't interfere with stdio protocol
	fmt.Fprintf(os.Stderr, "OneClickVirt MCP server started. Waiting for JSON-RPC requests on stdin...\n")
	fmt.Fprintf(os.Stderr, "API URL: %s\n", *apiURL)

	return server.RunStdio()
}

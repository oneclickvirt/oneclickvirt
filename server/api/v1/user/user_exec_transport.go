package user

import (
	providerModel "oneclickvirt/model/provider"
	consoleService "oneclickvirt/service/console"

	"github.com/gorilla/websocket"
)

// These compatibility wrappers keep existing Web Exec callers on the shared
// terminal transport used by every console access scope.
func handleAgentExecTerminal(ws *websocket.Conn, providerID uint, command string) {
	consoleService.ProxyTerminalWebSocket(ws, consoleService.TerminalTarget{
		ProviderID: providerID, ConnectionType: "agent", Command: command,
	})
}

func handleLocalExecTerminal(ws *websocket.Conn, command string) {
	consoleService.ProxyTerminalWebSocket(ws, consoleService.TerminalTarget{
		ConnectionType: "local", Command: command,
	})
}

func handleSSHCommandTerminal(ws *websocket.Conn, provider providerModel.Provider, command string) {
	consoleService.ProxyTerminalWebSocket(ws, consoleService.TerminalTarget{
		ConnectionType: "ssh", Provider: provider, Command: command,
	})
}

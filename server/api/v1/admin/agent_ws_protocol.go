package admin

import (
	"context"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// Browser mixed-content rules prevent HTTPS pages from probing WS. Probe the
// public controller endpoint without credentials, outside database operations.
// Keep the inferred URL when neither protocol can be verified from this host.
func selectAgentWebSocketURL(ctx context.Context, inferred string, dialer *websocket.Dialer) string {
	endpoint, err := url.Parse(inferred)
	if err != nil || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Scheme != "ws" && endpoint.Scheme != "wss") {
		return inferred
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// A generated installer URL must never carry query credentials, including
	// when both protocol probes fail and we have to keep the inferred scheme.
	// The Agent sends its secret in Authorization/X-Agent-Secret headers.
	fallback := *endpoint
	fallback.RawQuery = ""
	fallback.Fragment = ""
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	probeDialer := *dialer
	probeDialer.HandshakeTimeout = 3 * time.Second
	for _, scheme := range []string{"wss", "ws"} {
		if ctx.Err() != nil {
			return fallback.String()
		}
		probe := *endpoint
		probe.Scheme = scheme
		probe.RawQuery = "probe=1"
		probe.Fragment = ""
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		conn, response, probeErr := probeDialer.DialContext(probeCtx, probe.String(), nil)
		cancel()
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if conn != nil {
			_ = conn.Close()
		}
		if probeErr == nil && ctx.Err() == nil {
			endpoint.Scheme = scheme
			// The probe query is only for this anonymous handshake. Never return
			// it to the installer: the real Agent connection must authenticate
			// with its Authorization header and follow the normal handler path.
			endpoint.RawQuery = ""
			endpoint.Fragment = ""
			return endpoint.String()
		}
	}
	return fallback.String()
}

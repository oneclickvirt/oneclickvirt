// WebSocket reverse-connect client for NAT traversal (agent mode).
// The controller acts as WebSocket server; the agent connects back and handles
// exec_req / ping / info / tunnel_open frames.

mod handler;
mod shell;
mod types;

use regex;
use std::io;
use std::time::Duration;
use tokio::net::TcpStream;
use tokio_tungstenite::tungstenite::{ClientRequestBuilder, http::Uri};
use tokio_tungstenite::{client_async, connect_async};
use tracing::{info, warn};
use url;

/// Derive a browser-like Origin header value from a WebSocket URL.
/// wss://host:port/path → https://host:port
/// ws://host:port/path  → http://host:port
fn origin_from_ws_url(url_str: &str) -> String {
    if let Ok(parsed) = url::Url::parse(url_str) {
        let scheme = if parsed.scheme() == "wss" {
            "https"
        } else {
            "http"
        };
        let host = parsed.host_str().unwrap_or("localhost");
        if let Some(port) = parsed.port() {
            format!("{}://{}:{}", scheme, host, port)
        } else {
            format!("{}://{}", scheme, host)
        }
    } else {
        "https://localhost".to_string()
    }
}

/// A realistic Chrome browser User-Agent string (Linux x86_64).
const BROWSER_UA: &str = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36";

use handler::handle_connection;

pub async fn run_ws_client(ws_url: String, secret: String) {
    // Strip any sensitive query params from the URL for security.
    // The secret is transmitted via Authorization header instead.
    let clean_url = strip_secret_params(&ws_url);

    // Track whether we have already tried the ws:// fallback (to avoid loops).
    let mut ws_fallback_tried = false;

    let mut delay_secs: u64 = 2;
    loop {
        let connect_url = if ws_fallback_tried {
            // Already fell back to ws://, use it directly on subsequent attempts.
            clean_url.replacen("wss://", "ws://", 1)
        } else {
            clean_url.clone()
        };

        info!(url = %connect_url, "connecting to controller via WebSocket");

        // For plain ws:// connections, use connect_plain_with_keepalive
        // which configures TCP keepalive (30s idle, 10s interval, 3 probes)
        // on the underlying socket for transport-layer dead-connection detection.
        // For wss:// connections, use the standard connect_async (TLS handled
        // internally by tokio-tungstenite).
        if connect_url.starts_with("wss://") {
            let uri: Uri = match connect_url.parse() {
                Ok(u) => u,
                Err(e) => {
                    warn!(error = %e, "invalid WebSocket URI, retrying");
                    tokio::time::sleep(tokio::time::Duration::from_secs(delay_secs)).await;
                    if delay_secs < 60 {
                        delay_secs = (delay_secs * 2).min(60);
                    }
                    continue;
                }
            };
            let origin = origin_from_ws_url(&connect_url);
            let request = ClientRequestBuilder::new(uri)
                .with_header("Authorization", format!("Bearer {}", secret))
                .with_header("User-Agent", BROWSER_UA)
                .with_header("Origin", origin);
            match connect_async(request).await {
                Ok((ws_stream, _)) => {
                    // Set TCP keepalive on the underlying socket even for
                    // wss:// connections (TLS wraps the TCP stream but the
                    // kernel-level keepalive still applies).
                    try_set_keepalive_on_maybe_tls(&ws_stream);
                    info!("WebSocket connected to controller");
                    delay_secs = 2;
                    if let Err(e) = handle_connection(ws_stream, &secret).await {
                        warn!(error = %e, "WebSocket connection closed with error");
                    } else {
                        info!("WebSocket connection closed normally");
                    }
                }
                Err(e) => {
                    let err_msg = e.to_string();
                    if !ws_fallback_tried
                        && clean_url.starts_with("wss://")
                        && is_tls_layer_error(&err_msg)
                    {
                        warn!(
                            error = %e,
                            "wss:// failed with TLS error, falling back to ws:// (plain WebSocket)"
                        );
                        ws_fallback_tried = true;
                        delay_secs = 1;
                        continue;
                    }
                    warn!(error = %e, delay_secs, "failed to connect to controller, retrying");
                }
            }
        } else {
            // Plain ws:// with TCP keepalive configured
            match connect_plain_with_keepalive(&connect_url, &secret).await {
                Ok(ws_stream) => {
                    info!("WebSocket connected to controller");
                    delay_secs = 2;
                    if let Err(e) = handle_connection(ws_stream, &secret).await {
                        warn!(error = %e, "WebSocket connection closed with error");
                    } else {
                        info!("WebSocket connection closed normally");
                    }
                }
                Err(e) => {
                    warn!(error = %e, delay_secs, "failed to connect to controller, retrying");
                }
            }
        }
        tokio::time::sleep(tokio::time::Duration::from_secs(delay_secs)).await;
        if delay_secs < 60 {
            delay_secs = (delay_secs * 2).min(60);
        }
    }
}

/// Check whether an error message indicates a TLS-layer failure (as opposed
/// to an HTTP-level or application-level error).  When these patterns appear
/// on a wss:// connection it usually means the server is plain HTTP — not TLS.
fn is_tls_layer_error(err_msg: &str) -> bool {
    let lower = err_msg.to_lowercase();
    lower.contains("invalidcontenttype")
        || lower.contains("corrupt message")
        || lower.contains("tls")
        || lower.contains("ssl")
        || lower.contains("certificate")
        || lower.contains("handshake")
        || lower.contains("bad record mac")
        || lower.contains("unknown protocol")
        || lower.contains("peer misbehaving")
}

/// Strip sensitive query parameters (secret, agent_secret, token) from a URL.
/// Defense-in-depth: even if the caller passes a URL with the secret embedded,
/// we remove it before using the URL in the HTTP request or logging.
fn strip_secret_params(url_str: &str) -> String {
    if !url_str.contains('?') {
        return url_str.to_string();
    }

    // Use the `url` crate for robust parsing
    if let Ok(mut parsed) = url::Url::parse(url_str) {
        let sensitive: &[&str] = &["secret", "agent_secret", "token"];
        let had = sensitive
            .iter()
            .any(|k| parsed.query_pairs().any(|(qk, _)| *k == qk.as_ref()));
        if !had {
            return url_str.to_string();
        }
        let new_query: Vec<String> = parsed
            .query_pairs()
            .filter(|(k, _)| !sensitive.iter().any(|sk| *sk == k.as_ref()))
            .map(|(k, v)| format!("{}={}", k, v))
            .collect();
        if new_query.is_empty() {
            parsed.set_query(None);
        } else {
            parsed.set_query(Some(&new_query.join("&")));
        }
        return parsed.to_string();
    }

    // Fallback: simple regex-based strip
    let re = regex::Regex::new(r"[&?](secret|agent_secret|token)=[^&]*").unwrap();
    re.replace_all(url_str, "")
        .replace("?&", "?")
        .trim_end_matches('?')
        .to_string()
}
/// Set TCP keepalive on the underlying socket of a WebSocketStream that
/// wraps a MaybeTlsStream<TcpStream>.  Works for both Plain (ws://) and
/// Rustls (wss://) variants by extracting the raw file descriptor.
///
/// This is a best-effort operation: failures are silently ignored since
/// keepalive is a defense-in-depth measure, not a correctness requirement.
#[cfg(unix)]
fn try_set_keepalive_on_maybe_tls(
    ws: &tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>,
) {
    use tokio_tungstenite::MaybeTlsStream;

    let inner = ws.get_ref();
    match inner {
        MaybeTlsStream::Plain(tcp) => {
            set_keepalive_on_tcp(tcp);
        }
        MaybeTlsStream::Rustls(tls) => {
            // tokio_rustls::TlsStream<TcpStream>::get_ref() returns
            // &(TcpStream, SessionState) — the TcpStream is in .0
            let (tcp, _) = tls.get_ref();
            set_keepalive_on_tcp(tcp);
        }
        _ => {}
    }
}

/// Configure TCP keepalive on a tokio TcpStream using socket2.
#[cfg(unix)]
fn set_keepalive_on_tcp(stream: &TcpStream) {
    use socket2::SockRef;
    let sock = SockRef::from(stream);
    let _ = sock.set_keepalive(true);
    #[cfg(target_os = "linux")]
    {
        let ka = socket2::TcpKeepalive::new()
            .with_time(Duration::from_secs(30))
            .with_interval(Duration::from_secs(10));
        // Note: socket2 0.5 does not expose TCP_KEEPCNT (retries);
        // the OS default (typically 9 on Linux) is sufficient.
        let _ = sock.set_tcp_keepalive(&ka);
    }
    #[cfg(not(target_os = "linux"))]
    {
        let ka = socket2::TcpKeepalive::new().with_time(Duration::from_secs(30));
        let _ = sock.set_tcp_keepalive(&ka);
    }
}

/// no-op on non-Unix platforms (keepalive not supported).
#[cfg(not(unix))]
fn try_set_keepalive_on_maybe_tls(
    _ws: &tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>,
) {
}

#[cfg(not(unix))]
fn set_keepalive_on_tcp(_stream: &TcpStream) {}
/// Create a TCP stream with keepalive configured using tokio's async connect.
///
/// TCP keepalive parameters (applied after connect via socket2::SockRef):
///   - idle:  30 s  (send first probe after 30 s of silence)
///   - interval: 10 s  (wait 10 s between probes)
///
/// Total detection time: 30 + 10*3 = 60 s worst case (with OS default retries=3).
///
/// IMPORTANT: We use tokio::net::TcpStream::connect() (async) instead of
/// socket2::Socket::connect() on a non-blocking socket.  The latter returns
/// EINPROGRESS immediately on Linux — the TCP handshake has not completed
/// yet — which causes every connection attempt to fail.
///
/// `addr` must be a `"host:port"` string.  IPv6 addresses must be wrapped in
/// brackets, e.g. `"[::1]:8080"`.  Hostnames are resolved via tokio's async
/// DNS resolver (getaddrinfo).  Do NOT pass a bare `std::net::SocketAddr` —
/// SocketAddr::parse() rejects hostnames and would break non-IP deployments.
async fn create_tcp_stream_with_keepalive(addr: &str) -> io::Result<TcpStream> {
    let stream = TcpStream::connect(addr).await?;

    // Configure TCP keepalive on the underlying socket via socket2::SockRef.
    // tokio::net::TcpStream implements AsRawFd on Unix, so SockRef can
    // operate on its fd directly.
    use socket2::SockRef;
    let sock_ref = SockRef::from(&stream);
    sock_ref.set_keepalive(true)?;

    // Configure TCP keepalive timing.
    // On Linux: full configuration with idle time and probe interval.
    // On macOS: idle time only (TCP_KEEPALIVE), interval not settable via socket2.
    #[cfg(target_os = "linux")]
    {
        let ka = socket2::TcpKeepalive::new()
            .with_time(Duration::from_secs(30))
            .with_interval(Duration::from_secs(10));
        // Note: socket2 0.5 does not expose TCP_KEEPCNT (retries);
        // the OS default (typically 9 on Linux) is sufficient.
        let _ = sock_ref.set_tcp_keepalive(&ka);
    }
    #[cfg(not(target_os = "linux"))]
    {
        // Fallback: set idle time via TcpKeepalive (interval uses OS defaults)
        let ka = socket2::TcpKeepalive::new().with_time(Duration::from_secs(30));
        let _ = sock_ref.set_tcp_keepalive(&ka);
    }

    Ok(stream)
}

/// Connect to the controller WebSocket with TCP keepalive configured
/// (for plain ws:// connections only).  wss:// connections use the
/// standard connect_async path which handles TLS internally.
async fn connect_plain_with_keepalive(
    url_str: &str,
    secret: &str,
) -> Result<tokio_tungstenite::WebSocketStream<TcpStream>, String> {
    let parsed_url = url::Url::parse(url_str).map_err(|e| format!("invalid URL: {e}"))?;
    let host = parsed_url
        .host_str()
        .ok_or_else(|| "missing host in URL".to_string())?;
    let port = parsed_url.port().unwrap_or(80);
    // Build the "host:port" string for tokio's async connect, which handles
    // DNS resolution, IPv4, and IPv6.  URL parsing strips IPv6 brackets, so
    // we restore them when the host contains ':', otherwise a bare
    // "::1:8080" string would be misparse by ToSocketAddrs.
    let addr_str = if host.contains(':') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    };

    let tcp_stream = create_tcp_stream_with_keepalive(&addr_str)
        .await
        .map_err(|e| format!("TCP connect failed: {e}"))?;

    // Parse the full URL as the request URI (same approach as the wss://
    // path using connect_async).  http::Uri extracts the Host header and
    // request path from the full URL automatically.
    let request_uri: Uri = url_str.parse().map_err(|e| format!("invalid URI: {e}"))?;

    let origin = origin_from_ws_url(url_str);
    let request = ClientRequestBuilder::new(request_uri)
        .with_header("Authorization", format!("Bearer {}", secret))
        .with_header("User-Agent", BROWSER_UA)
        .with_header("Origin", origin);

    let (ws_stream, _) = client_async(request, tcp_stream)
        .await
        .map_err(|e| format!("WebSocket handshake failed: {e}"))?;
    Ok(ws_stream)
}

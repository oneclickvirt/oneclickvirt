use axum::{
    body::Body,
    extract::{Host, Request, State},
    http::{HeaderValue, StatusCode, Uri, Version},
    response::{IntoResponse, Response},
};
use hyper_util::{
    client::legacy::Client,
    rt::{TokioExecutor, TokioIo},
};
use std::sync::RwLock as StdRwLock;
use std::{collections::HashMap, env, io::BufReader, sync::Arc};
use tokio::sync::RwLock;
use tracing::{debug, error, info, warn};

use crate::app_state::AppState;

use rustls_pemfile::{certs, private_key};
use tokio_rustls::rustls::{
    self,
    pki_types::CertificateDer,
    server::{ClientHello, ResolvesServerCert},
    sign::CertifiedKey,
};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ProxyTarget {
    pub internal_ip: String,
    pub internal_port: u16,
    pub protocol: String,
}

fn upstream_authority(host: &str, port: u16) -> String {
    let host = host.trim();
    let host = host
        .strip_prefix('[')
        .and_then(|value| value.strip_suffix(']'))
        .unwrap_or(host);
    if host.contains(':') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    }
}

pub type ProxyRoutes = Arc<RwLock<HashMap<String, ProxyTarget>>>;

/// Thread-safe cert store for per-domain TLS certificates (uses std RwLock for sync ResolvesServerCert)
pub type CertStore = Arc<StdRwLock<HashMap<String, Arc<CertifiedKey>>>>;

/// SNI-based cert resolver that picks per-domain certs with fallback to default
#[derive(Debug)]
pub struct DomainCertResolver {
    pub default_cert: Option<Arc<CertifiedKey>>,
    pub domain_certs: CertStore,
}

impl ResolvesServerCert for DomainCertResolver {
    fn resolve(&self, client_hello: ClientHello<'_>) -> Option<Arc<CertifiedKey>> {
        if let Some(domain) = client_hello.server_name() {
            let domain = domain.to_lowercase();
            if let Ok(certs) = self.domain_certs.read()
                && let Some(cert) = certs.get(&domain)
            {
                return Some(cert.clone());
            }
        }
        self.default_cert.clone()
    }
}

/// Parse PEM-encoded cert chain and private key into a CertifiedKey
pub fn parse_certified_key(cert_pem: &str, key_pem: &str) -> Result<CertifiedKey, String> {
    use std::io::Cursor;

    let mut cert_reader = BufReader::new(Cursor::new(cert_pem.as_bytes()));
    let cert_chain: Vec<CertificateDer> = certs(&mut cert_reader)
        .collect::<Result<_, _>>()
        .map_err(|e| format!("parse cert: {e}"))?;

    if cert_chain.is_empty() {
        return Err("no certificates found in PEM".into());
    }

    let mut key_reader = BufReader::new(Cursor::new(key_pem.as_bytes()));
    let key_der = private_key(&mut key_reader)
        .map_err(|e| format!("parse key: {e}"))?
        .ok_or_else(|| "no private key found in PEM".to_string())?;

    let provider = rustls::crypto::ring::default_provider();
    let signing_key = provider
        .key_provider
        .load_private_key(key_der)
        .map_err(|e| format!("load signing key: {e}"))?;

    Ok(CertifiedKey::new(cert_chain, signing_key))
}

/// Load domain certificates from DB into a cert store
pub fn load_domain_certs_from_db(
    conn: &rusqlite::Connection,
) -> HashMap<String, Arc<CertifiedKey>> {
    let mut certs = HashMap::new();
    let mut stmt = match conn.prepare(
        "SELECT domain, ssl_cert, ssl_key FROM domain_proxies WHERE enable_ssl = 1 AND ssl_cert != '' AND ssl_key != ''"
    ) {
        Ok(s) => s,
        Err(e) => {
            warn!(error = %e, "failed to prepare domain certs query");
            return certs;
        }
    };

    let rows = match stmt.query_map([], |row| {
        Ok((
            row.get::<_, String>(0)?,
            row.get::<_, String>(1)?,
            row.get::<_, String>(2)?,
        ))
    }) {
        Ok(r) => r,
        Err(e) => {
            warn!(error = %e, "failed to query domain certs");
            return certs;
        }
    };

    for (domain, cert_pem, key_pem) in rows.flatten() {
        let domain = domain.trim().to_lowercase();
        if domain.is_empty() {
            continue;
        }
        match parse_certified_key(&cert_pem, &key_pem) {
            Ok(ck) => {
                info!(domain = %domain, "loaded domain certificate");
                certs.insert(domain, Arc::new(ck));
            }
            Err(e) => {
                warn!(domain = %domain, error = %e, "failed to parse domain certificate");
            }
        }
    }

    info!(
        count = certs.len(),
        "loaded domain certificates from database"
    );
    certs
}

/// Load all domain proxies from database into memory
pub async fn load_routes_from_db(state: &AppState) -> Result<HashMap<String, ProxyTarget>, String> {
    let conn = state.conn.lock().await;
    let mut stmt = conn
        .prepare("SELECT domain, internal_ip, internal_port, protocol FROM domain_proxies")
        .map_err(|e| format!("prepare proxy routes query: {e}"))?;

    let rows = stmt
        .query_map([], |row| {
            Ok((
                row.get::<_, String>(0)?,
                ProxyTarget {
                    internal_ip: row.get(1)?,
                    internal_port: row.get(2)?,
                    protocol: row.get(3)?,
                },
            ))
        })
        .map_err(|e| format!("query proxy routes: {e}"))?;

    let mut routes = HashMap::new();
    for row in rows {
        let (domain, target) = row.map_err(|e| format!("parse proxy route: {e}"))?;
        let domain = domain.trim().to_lowercase();
        if domain.is_empty() {
            continue;
        }
        routes.insert(domain, target);
    }

    info!(count = routes.len(), "loaded proxy routes from database");
    Ok(routes)
}

/// Add or update a proxy route in memory
pub async fn add_route(routes: &ProxyRoutes, domain: String, target: ProxyTarget) {
    let mut map = routes.write().await;
    let domain = domain.to_lowercase();
    if map.get(&domain) == Some(&target) {
        return;
    }
    map.insert(domain.clone(), target);
    info!(domain = %domain, "proxy route added to memory");
}

/// Remove a proxy route from memory
pub async fn remove_route(routes: &ProxyRoutes, domain: &str) -> bool {
    let mut map = routes.write().await;
    let domain = domain.to_lowercase();
    let removed = map.remove(&domain).is_some();
    if removed {
        info!(domain = %domain, "proxy route removed from memory");
    }
    removed
}

/// Get a proxy target by domain
pub async fn get_route(routes: &ProxyRoutes, domain: &str) -> Option<ProxyTarget> {
    let map = routes.read().await;
    map.get(&domain.to_lowercase()).cloned()
}

/// Main proxy handler
pub async fn proxy_handler(
    Host(host): Host,
    State(routes): State<ProxyRoutes>,
    req: Request,
) -> Response {
    proxy_handler_with_scheme(host, routes, req, "http").await
}

/// HTTPS listener entry point. The listener is selected before this handler is
/// invoked, so the forwarded scheme must be supplied explicitly rather than
/// inferred from the request URI (which is usually origin-form and has no
/// scheme component).
pub async fn proxy_https_handler(
    Host(host): Host,
    State(routes): State<ProxyRoutes>,
    req: Request,
) -> Response {
    proxy_handler_with_scheme(host, routes, req, "https").await
}

async fn proxy_handler_with_scheme(
    host: String,
    routes: ProxyRoutes,
    mut req: Request,
    forwarded_proto: &'static str,
) -> Response {
    // Extract domain from Host header (remove port if present)
    let domain = host.split(':').next().unwrap_or(&host).to_lowercase();

    debug!(domain = %domain, "proxy request received");

    // Look up the target
    let target = match get_route(&routes, &domain).await {
        Some(t) => t,
        None => {
            warn!(domain = %domain, "no proxy route found for domain");
            return (
                StatusCode::NOT_FOUND,
                format!("No proxy configured for domain: {}", domain),
            )
                .into_response();
        }
    };

    // Build upstream URL
    let upstream_url = format!(
        "{}://{}",
        target.protocol,
        upstream_authority(&target.internal_ip, target.internal_port)
    );

    // Parse the request URI and build the full upstream path
    let path_and_query = req
        .uri()
        .path_and_query()
        .map(|pq| pq.as_str())
        .unwrap_or("/");

    let upstream_uri = match format!("{}{}", upstream_url, path_and_query).parse::<Uri>() {
        Ok(uri) => uri,
        Err(e) => {
            error!(error = %e, "failed to parse upstream URI");
            return (StatusCode::INTERNAL_SERVER_ERROR, "Invalid upstream URI").into_response();
        }
    };

    debug!(upstream = %upstream_uri, "forwarding request");

    let websocket_upgrade = is_websocket_upgrade(&req);
    // The origin connection is independent of the browser connection.  In
    // particular, an HTTP/2 request accepted by our TLS listener may target a
    // plain HTTP/1.1 container.  Hyper rejects that version/origin pairing
    // with UserUnsupportedVersion instead of negotiating it.  Use HTTP/1.1
    // upstream for ordinary requests as well as WebSocket upgrades.
    *req.version_mut() = Version::HTTP_11;

    // Update request URI
    *req.uri_mut() = upstream_uri.clone();

    // Update Host header to match upstream
    if let Ok(authority) = upstream_authority(&target.internal_ip, target.internal_port).parse() {
        req.headers_mut().insert(hyper::header::HOST, authority);
    }

    // A direct client can forge X-Forwarded-Proto, so the listener scheme is
    // authoritative by default.  When this Agent is explicitly placed behind
    // Cloudflare, CF-Visitor is the only additional signal we accept.  This is
    // opt-in because trusting it on a directly exposed listener would let a
    // client manufacture an HTTPS scheme and trigger redirect/cookie changes.
    let forwarded_proto =
        effective_forwarded_proto(&req, forwarded_proto, cloudflare_headers_enabled());

    // Add X-Forwarded headers
    let headers = req.headers_mut();
    headers.insert(
        "X-Forwarded-Host",
        HeaderValue::from_str(&host).unwrap_or_else(|_| HeaderValue::from_static("")),
    );
    headers.insert(
        "X-Forwarded-Proto",
        HeaderValue::from_static(forwarded_proto),
    );

    // Create HTTP client
    let connector = hyper_rustls::HttpsConnectorBuilder::new()
        .with_webpki_roots()
        .https_or_http()
        .enable_http1()
        .build();
    let client: Client<_, Body> = Client::builder(TokioExecutor::new()).build(connector);

    // Forward the request
    let client_upgrade = websocket_upgrade.then(|| hyper::upgrade::on(&mut req));
    match client.request(req).await {
        Ok(mut response)
            if websocket_upgrade && response.status() == StatusCode::SWITCHING_PROTOCOLS =>
        {
            let upstream_upgrade = hyper::upgrade::on(&mut response);
            let (parts, _body) = response.into_parts();
            if let Some(client_upgrade) = client_upgrade {
                tokio::spawn(async move {
                    match (client_upgrade.await, upstream_upgrade.await) {
                        (Ok(client_io), Ok(upstream_io)) => {
                            let mut client_io = TokioIo::new(client_io);
                            let mut upstream_io = TokioIo::new(upstream_io);
                            if let Err(error) =
                                tokio::io::copy_bidirectional(&mut client_io, &mut upstream_io)
                                    .await
                            {
                                debug!(%error, "websocket proxy tunnel closed with an I/O error");
                            }
                        }
                        (Err(error), _) => {
                            debug!(%error, "client websocket upgrade failed");
                        }
                        (_, Err(error)) => {
                            debug!(%error, "upstream websocket upgrade failed");
                        }
                    }
                });
            }
            Response::from_parts(parts, Body::empty())
        }
        Ok(response) => {
            debug!(status = %response.status(), "upstream responded");
            response.into_response()
        }
        Err(e) => {
            error!(error = %e, upstream = %upstream_uri, "upstream request failed");
            (
                StatusCode::BAD_GATEWAY,
                format!("Failed to connect to upstream: {}", e),
            )
                .into_response()
        }
    }
}

/// Return the scheme that the upstream application should see.
///
/// Cloudflare Flexible SSL terminates HTTPS at the edge and uses HTTP for the
/// origin hop. In that one configuration the origin listener is HTTP while the
/// browser scheme is still HTTPS; trusting a valid CF-Visitor header avoids a
/// redirect loop. Full/Full (strict) use an HTTPS origin listener and therefore
/// remain authoritative even when this option is enabled.
fn effective_forwarded_proto(
    req: &Request,
    listener_proto: &'static str,
    trust_cloudflare_headers: bool,
) -> &'static str {
    if listener_proto.eq_ignore_ascii_case("https") {
        return "https";
    }
    if trust_cloudflare_headers
        && req
            .headers()
            .get("cf-visitor")
            .and_then(cloudflare_visitor_scheme)
            == Some("https")
    {
        return "https";
    }
    "http"
}

fn cloudflare_visitor_scheme(value: &HeaderValue) -> Option<&'static str> {
    let raw = value.to_str().ok()?.trim();
    let parsed: serde_json::Value = serde_json::from_str(raw).ok()?;
    match parsed
        .get("scheme")?
        .as_str()?
        .to_ascii_lowercase()
        .as_str()
    {
        "https" => Some("https"),
        "http" => Some("http"),
        _ => None,
    }
}

fn cloudflare_headers_enabled() -> bool {
    env::var("PROXY_TRUST_CLOUDFLARE_HEADERS")
        .or_else(|_| env::var("TRUST_CLOUDFLARE_HEADERS"))
        .map(|value| {
            matches!(
                value.trim().to_ascii_lowercase().as_str(),
                "1" | "true" | "yes" | "on"
            )
        })
        .unwrap_or(false)
}

fn header_contains_token(value: Option<&HeaderValue>, token: &str) -> bool {
    value
        .and_then(|value| value.to_str().ok())
        .map(|value| {
            value
                .split(',')
                .any(|part| part.trim().eq_ignore_ascii_case(token))
        })
        .unwrap_or(false)
}

fn is_websocket_upgrade(req: &Request) -> bool {
    header_contains_token(req.headers().get(hyper::header::CONNECTION), "upgrade")
        && req
            .headers()
            .get(hyper::header::UPGRADE)
            .and_then(|value| value.to_str().ok())
            .map(|value| value.trim().eq_ignore_ascii_case("websocket"))
            .unwrap_or(false)
}

#[cfg(test)]
mod tests {
    use super::{
        ProxyRoutes, ProxyTarget, effective_forwarded_proto, is_websocket_upgrade,
        upstream_authority,
    };
    use axum::{
        Router,
        body::Body,
        extract::{Host, State},
        http::{HeaderMap, Request, Version, header},
        routing::any,
    };
    use futures_util::{SinkExt, StreamExt};
    use http_body_util::BodyExt;
    use hyper::Uri;
    use std::{collections::HashMap, sync::Arc, time::Duration};
    use tokio::sync::RwLock;
    use tokio_tungstenite::{
        accept_async, connect_async,
        tungstenite::{Message, client::IntoClientRequest},
    };

    #[test]
    fn formats_ipv4_hostname_and_ipv6_authorities() {
        assert_eq!(upstream_authority("192.0.2.10", 8080), "192.0.2.10:8080");
        assert_eq!(
            upstream_authority("backend.internal", 8080),
            "backend.internal:8080"
        );
        assert_eq!(
            upstream_authority("2001:db8::10", 8080),
            "[2001:db8::10]:8080"
        );
        assert_eq!(
            upstream_authority("[2001:db8::10]", 8443),
            "[2001:db8::10]:8443"
        );
        let uri: Uri = format!("http://{}", upstream_authority("2001:db8::10", 8080))
            .parse()
            .expect("IPv6 upstream authority must produce a valid URI");
        assert_eq!(uri.host(), Some("[2001:db8::10]"));
        assert_eq!(uri.port_u16(), Some(8080));
    }

    #[test]
    fn detects_case_insensitive_websocket_upgrade_tokens() {
        let request = Request::builder()
            .header(header::CONNECTION, "keep-alive, Upgrade")
            .header(header::UPGRADE, "WebSocket")
            .body(Body::empty())
            .unwrap();
        assert!(is_websocket_upgrade(&request));

        let request = Request::builder()
            .header(header::CONNECTION, "keep-alive")
            .header(header::UPGRADE, "websocket")
            .body(Body::empty())
            .unwrap();
        assert!(!is_websocket_upgrade(&request));
    }

    #[test]
    fn cloudflare_scheme_is_opt_in_and_listener_https_wins() {
        let request = Request::builder()
            .header("CF-Visitor", r#"{"scheme":"https"}"#)
            .body(Body::empty())
            .unwrap();
        assert_eq!(effective_forwarded_proto(&request, "http", false), "http");
        assert_eq!(effective_forwarded_proto(&request, "http", true), "https");
        assert_eq!(effective_forwarded_proto(&request, "https", false), "https");
    }

    #[test]
    fn invalid_or_http_cloudflare_headers_do_not_upgrade_origin_scheme() {
        let request = Request::builder()
            .header("CF-Visitor", r#"{"scheme":"http"}"#)
            .body(Body::empty())
            .unwrap();
        assert_eq!(effective_forwarded_proto(&request, "http", true), "http");

        let malformed = Request::builder()
            .header("CF-Visitor", "not-json")
            .body(Body::empty())
            .unwrap();
        assert_eq!(effective_forwarded_proto(&malformed, "http", true), "http");
    }

    #[tokio::test]
    async fn https_listener_handler_forwards_https_scheme() {
        let _ = rustls::crypto::ring::default_provider().install_default();
        let upstream_listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let upstream_addr = upstream_listener.local_addr().unwrap();
        let upstream_task = tokio::spawn(async move {
            axum::serve(
                upstream_listener,
                Router::new().fallback(any(|headers: HeaderMap| async move {
                    headers
                        .get("x-forwarded-proto")
                        .and_then(|value| value.to_str().ok())
                        .unwrap_or("missing")
                        .to_string()
                })),
            )
            .await
            .unwrap();
        });
        let routes: ProxyRoutes = Arc::new(RwLock::new(HashMap::from([(
            "proxy.test".to_string(),
            ProxyTarget {
                internal_ip: "127.0.0.1".to_string(),
                internal_port: upstream_addr.port(),
                protocol: "http".to_string(),
            },
        )])));
        let request = Request::builder()
            .uri("/scheme")
            .body(Body::empty())
            .unwrap();
        let response =
            super::proxy_https_handler(Host("proxy.test".to_string()), State(routes), request)
                .await;
        assert_eq!(response.status(), axum::http::StatusCode::OK);
        let body = response.into_body().collect().await.unwrap().to_bytes();
        assert_eq!(body.as_ref(), b"https");
        upstream_task.abort();
    }

    #[tokio::test]
    async fn forwards_http2_client_request_to_http1_origin() {
        let _ = rustls::crypto::ring::default_provider().install_default();
        let upstream_listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let upstream_addr = upstream_listener.local_addr().unwrap();
        let upstream_task = tokio::spawn(async move {
            axum::serve(
                upstream_listener,
                Router::new().fallback(any(|req: Request<Body>| async move {
                    format!("{:?}", req.version())
                })),
            )
            .await
            .unwrap();
        });
        let routes: ProxyRoutes = Arc::new(RwLock::new(HashMap::from([(
            "proxy.test".to_string(),
            ProxyTarget {
                internal_ip: "127.0.0.1".to_string(),
                internal_port: upstream_addr.port(),
                protocol: "http".to_string(),
            },
        )])));
        let request = Request::builder()
            .version(Version::HTTP_2)
            .uri("/from-http2")
            .body(Body::empty())
            .unwrap();
        let response =
            super::proxy_https_handler(Host("proxy.test".to_string()), State(routes), request)
                .await;
        assert_eq!(response.status(), axum::http::StatusCode::OK);
        let body = response.into_body().collect().await.unwrap().to_bytes();
        assert_eq!(body.as_ref(), b"HTTP/1.1");
        upstream_task.abort();
    }

    #[tokio::test]
    async fn proxies_websocket_upgrade_and_bidirectional_frames() {
        let _ = rustls::crypto::ring::default_provider().install_default();
        let upstream_listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let upstream_addr = upstream_listener.local_addr().unwrap();
        let upstream_task = tokio::spawn(async move {
            let (stream, _) = upstream_listener.accept().await.unwrap();
            let mut websocket = accept_async(stream).await.unwrap();
            while let Some(message) = websocket.next().await {
                let message = message.unwrap();
                let close = message.is_close();
                websocket.send(message).await.unwrap();
                if close {
                    break;
                }
            }
        });

        let routes: ProxyRoutes = Arc::new(RwLock::new(HashMap::from([(
            "proxy.test".to_string(),
            ProxyTarget {
                internal_ip: "127.0.0.1".to_string(),
                internal_port: upstream_addr.port(),
                protocol: "http".to_string(),
            },
        )])));
        let proxy_listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let proxy_addr = proxy_listener.local_addr().unwrap();
        let proxy_task = tokio::spawn(async move {
            axum::serve(
                proxy_listener,
                Router::new()
                    .fallback(any(super::proxy_handler))
                    .with_state(routes),
            )
            .await
            .unwrap();
        });

        let mut request = format!("ws://127.0.0.1:{}/echo", proxy_addr.port())
            .into_client_request()
            .unwrap();
        request
            .headers_mut()
            .insert(header::HOST, "proxy.test".parse().unwrap());
        let (mut websocket, _) =
            tokio::time::timeout(Duration::from_secs(5), connect_async(request))
                .await
                .expect("proxy websocket handshake timed out")
                .expect("proxy websocket handshake failed");

        websocket.send(Message::Text("hello".into())).await.unwrap();
        assert_eq!(
            websocket.next().await.unwrap().unwrap(),
            Message::Text("hello".into())
        );
        websocket.close(None).await.unwrap();

        proxy_task.abort();
        upstream_task.abort();
    }
}

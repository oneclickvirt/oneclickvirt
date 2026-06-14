use axum::{
    body::Body,
    extract::{Host, Request, State},
    http::{HeaderValue, StatusCode, Uri},
    response::{IntoResponse, Response},
};
use hyper_util::{client::legacy::Client, rt::TokioExecutor};
use std::sync::RwLock as StdRwLock;
use std::{collections::HashMap, io::BufReader, sync::Arc};
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

#[derive(Clone, Debug)]
pub struct ProxyTarget {
    pub internal_ip: String,
    pub internal_port: u16,
    pub protocol: String,
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
            if let Ok(certs) = self.domain_certs.read() {
                if let Some(cert) = certs.get(&domain) {
                    return Some(cert.clone());
                }
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

    for row in rows {
        if let Ok((domain, cert_pem, key_pem)) = row {
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
    mut req: Request,
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
        "{}://{}:{}",
        target.protocol, target.internal_ip, target.internal_port
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

    // Update request URI
    *req.uri_mut() = upstream_uri.clone();

    // Update Host header to match upstream
    if let Ok(authority) = format!("{}:{}", target.internal_ip, target.internal_port).parse() {
        req.headers_mut().insert(hyper::header::HOST, authority);
    }

    // Add X-Forwarded headers
    let headers = req.headers_mut();
    headers.insert(
        "X-Forwarded-Host",
        HeaderValue::from_str(&host).unwrap_or_else(|_| HeaderValue::from_static("")),
    );
    headers.insert(
        "X-Forwarded-Proto",
        HeaderValue::from_static("http"), // TODO: detect if HTTPS
    );

    // Create HTTP client
    let connector = hyper_rustls::HttpsConnectorBuilder::new()
        .with_webpki_roots()
        .https_or_http()
        .enable_http1()
        .enable_http2()
        .build();
    let client: Client<_, Body> = Client::builder(TokioExecutor::new()).build(connector);

    // Forward the request
    match client.request(req).await {
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

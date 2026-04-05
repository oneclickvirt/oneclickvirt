use axum::{
    body::Body,
    extract::{Host, Request, State},
    http::{HeaderValue, StatusCode, Uri},
    response::{IntoResponse, Response},
};
use hyper_util::{
    client::legacy::{connect::HttpConnector, Client},
    rt::TokioExecutor,
};
use std::{collections::HashMap, sync::Arc};
use tokio::sync::RwLock;
use tracing::{debug, error, info, warn};

use crate::app_state::AppState;

#[derive(Clone, Debug)]
pub struct ProxyTarget {
    pub internal_ip: String,
    pub internal_port: u16,
    pub protocol: String,
}

pub type ProxyRoutes = Arc<RwLock<HashMap<String, ProxyTarget>>>;

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
        routes.insert(domain, target);
    }

    info!(count = routes.len(), "loaded proxy routes from database");
    Ok(routes)
}

/// Add or update a proxy route in memory
pub async fn add_route(routes: &ProxyRoutes, domain: String, target: ProxyTarget) {
    let mut map = routes.write().await;
    map.insert(domain.clone(), target);
    info!(domain = %domain, "proxy route added to memory");
}

/// Remove a proxy route from memory
pub async fn remove_route(routes: &ProxyRoutes, domain: &str) -> bool {
    let mut map = routes.write().await;
    let removed = map.remove(domain).is_some();
    if removed {
        info!(domain, "proxy route removed from memory");
    }
    removed
}

/// Get a proxy target by domain
pub async fn get_route(routes: &ProxyRoutes, domain: &str) -> Option<ProxyTarget> {
    let map = routes.read().await;
    map.get(domain).cloned()
}

/// Main proxy handler
pub async fn proxy_handler(
    Host(host): Host,
    State(routes): State<ProxyRoutes>,
    mut req: Request,
) -> Response {
    // Extract domain from Host header (remove port if present)
    let domain = host.split(':').next().unwrap_or(&host);

    debug!(domain, "proxy request received");

    // Look up the target
    let target = match get_route(&routes, domain).await {
        Some(t) => t,
        None => {
            warn!(domain, "no proxy route found for domain");
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
            return (StatusCode::INTERNAL_SERVER_ERROR, "Invalid upstream URI")
                .into_response();
        }
    };

    debug!(upstream = %upstream_uri, "forwarding request");

    // Update request URI
    *req.uri_mut() = upstream_uri.clone();

    // Update Host header to match upstream
    if let Ok(authority) = format!("{}:{}", target.internal_ip, target.internal_port).parse() {
        req.headers_mut()
            .insert(hyper::header::HOST, authority);
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
    let client: Client<HttpConnector, Body> =
        Client::builder(TokioExecutor::new()).build(HttpConnector::new());

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

mod app_state;
mod auth;
mod collector;
mod db;
mod docs;
mod error;
mod handlers;
mod ipt;
mod models;
mod nft;
mod proxy;
mod resource;
mod tunnel;
mod ws_client;

use app_state::AppState;
use axum::{Router, middleware, routing::{get, post}};
use collector::start_collector;
use db::init_db;
use docs::ApiDoc;
use rusqlite::Connection;
use std::{env, fs, io::BufReader, net::SocketAddr, path::Path, sync::Arc};
use tokio::sync::Mutex;
use tracing::{error, info, warn};
use tracing_subscriber::{EnvFilter, fmt};
use utoipa::OpenApi;
use utoipa_swagger_ui::SwaggerUi;

#[tokio::main]
async fn main() {
    fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .init();

    dotenvy::dotenv().ok();
    info!("loading configuration from environment");

    // --ws-url <URL> --secret <SECRET> launches agent-mode WS client instead of HTTP server
    let args: Vec<String> = std::env::args().collect();
    let ws_url_arg = args.windows(2).find(|w| w[0] == "--ws-url").map(|w| w[1].clone());
    let secret_arg = args.windows(2).find(|w| w[0] == "--secret").map(|w| w[1].clone());
    if let (Some(ws_url), Some(secret)) = (ws_url_arg, secret_arg) {
        // Append ?secret=... if not already present in URL
        let full_url = if ws_url.contains("secret=") {
            ws_url
        } else if ws_url.contains('?') {
            format!("{}&secret={}", ws_url, secret)
        } else {
            format!("{}?secret={}", ws_url, secret)
        };
        info!(url = %full_url, "starting in agent WebSocket client mode");
        ws_client::run_ws_client(full_url).await;
        return;
    }

    let api_token = env::var("API_TOKEN").expect("missing API_TOKEN in .env or environment");

    let traffic_collect_interval: u64 = env::var("TRAFFIC_COLLECT_INTERVAL")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(5);
    let resource_collect_interval: u64 = env::var("RESOURCE_COLLECT_INTERVAL")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(30);

    let traffic_collect_method = env::var("TRAFFIC_COLLECT_METHOD")
        .unwrap_or_else(|_| "nft".to_string());

    // Check if reverse proxy should be enabled
    let enable_proxy = env::var("ENABLE_REVERSE_PROXY")
        .unwrap_or_else(|_| "false".to_string())
        .parse::<bool>()
        .unwrap_or(false);

    info!(
        traffic_collect_interval,
        resource_collect_interval,
        %traffic_collect_method,
        enable_proxy,
        "collection intervals and proxy status configured"
    );

    info!("opening sqlite database");
    let conn = Connection::open("traffic.db").expect("failed to open sqlite database");
    init_db(&conn).expect("failed to init sqlite tables");
    info!("database initialized");

    if traffic_collect_method == "ipt" {
        info!("using iptables for traffic collection");
        if let Err(err) = ipt::bootstrap_from_db(&conn) {
            warn!(
                error = %err.message,
                "failed to bootstrap iptables counters"
            );
        }
        if let Err(err) = ipt::garbage_collect_orphans(&conn) {
            warn!(
                error = %err.message,
                "startup orphan iptables GC failed"
            );
        }
    } else {
        info!("using nftables for traffic collection");
        if let Err(err) = nft::bootstrap_from_db(&conn) {
            warn!(
                error = %err.message,
                "failed to bootstrap nft counters, external traffic stats may be unavailable until fixed"
            );
        }
        if let Err(err) = nft::garbage_collect_orphans(&conn) {
            warn!(
                error = %err.message,
                "startup orphan nft GC failed, old runtime rules may remain"
            );
        }
    }

    if traffic_collect_method == "ipt" {
        ipt::restore_block_rules();
    } else {
        nft::restore_block_rules();
    }

    // Initialize proxy routes from database
    info!("initializing reverse proxy routes");
    let proxy_routes = {
        let temp_state = AppState {
            conn: Arc::new(Mutex::new(conn)),
            api_token: api_token.clone(),
            traffic_collect_interval,
            resource_collect_interval,
            traffic_collect_method: traffic_collect_method.clone(),
            proxy_routes: Arc::new(tokio::sync::RwLock::new(std::collections::HashMap::new())),
            cert_store: Arc::new(std::sync::RwLock::new(std::collections::HashMap::new())),
        };
        let routes = proxy::load_routes_from_db(&temp_state)
            .await
            .unwrap_or_else(|e| {
                warn!(error = %e, "failed to load proxy routes, starting with empty routes");
                std::collections::HashMap::new()
            });

        Arc::new(tokio::sync::RwLock::new(routes))
    };

    // Load domain certificates from database
    info!("loading domain certificates");
    let cert_store = {
        let conn = Connection::open("traffic.db").expect("failed to open sqlite database");
        let certs = proxy::load_domain_certs_from_db(&conn);
        Arc::new(std::sync::RwLock::new(certs))
    };

    let conn = Connection::open("traffic.db").expect("failed to open sqlite database");
    let state = AppState {
        conn: Arc::new(Mutex::new(conn)),
        api_token,
        traffic_collect_interval,
        resource_collect_interval,
        traffic_collect_method,
        proxy_routes: proxy_routes.clone(),
        cert_store: cert_store.clone(),
    };

    start_collector(state.clone());

    let api_router = Router::new()
        .route("/api/v1/add", post(handlers::add_monitor))
        .route("/api/v1/update", post(handlers::update_monitor))
        .route("/api/v1/delete", post(handlers::delete_monitor))
        .route("/api/v1/info", post(handlers::info_monitor))
        .route("/api/v1/cleanup", post(handlers::cleanup_monitor))
        .route("/api/v1/resources", post(handlers::query_resources))
        .route("/api/v1/list", get(handlers::list_monitors))
        .route(
            "/api/v1/block-rules",
            post(handlers::apply_block_rules)
                .delete(handlers::remove_block_rules)
                .get(handlers::get_block_rules),
        )
        .route(
            "/api/v1/domain-proxy",
            post(handlers::add_domain_proxy)
                .delete(handlers::remove_domain_proxy)
                .get(handlers::list_domain_proxies),
        )
        .layer(middleware::from_fn_with_state(
            state.clone(),
            auth::require_token,
        ))
        .with_state(state);

    let app = api_router.merge(
        SwaggerUi::new("/swagger-ui").url("/api-docs/openapi.json", ApiDoc::openapi()),
    );

    // API server address
    let api_addr: SocketAddr = "0.0.0.0:23782".parse().expect("invalid bind address");
    info!(%api_addr, "starting API server");

    // Start API server
    let api_listener = tokio::net::TcpListener::bind(api_addr)
        .await
        .expect("failed to bind API server");

    if !enable_proxy {
        // Only run API server if proxy is disabled
        info!("reverse proxy disabled, running API server only");
        axum::serve(api_listener, app).await.expect("API server error");
    } else {
        // Start API server in background
        let api_server = tokio::spawn(async move {
            axum::serve(api_listener, app).await.expect("API server error");
        });

        // Reverse proxy configuration
        let proxy_http_addr: Option<SocketAddr> = env::var("PROXY_HTTP_ADDR")
            .ok()
            .and_then(|s| s.parse().ok());
        
        let proxy_https_addr: Option<SocketAddr> = env::var("PROXY_HTTPS_ADDR")
            .ok()
            .and_then(|s| s.parse().ok());

        let cert_path = env::var("PROXY_TLS_CERT").ok();
        let key_path = env::var("PROXY_TLS_KEY").ok();

        let proxy_router = Router::new()
            .fallback(proxy::proxy_handler)
            .with_state(proxy_routes);

        // Start proxy servers based on configuration
        match (proxy_http_addr, proxy_https_addr, cert_path, key_path) {
            // Only HTTP
            (Some(http_addr), None, _, _) => {
                info!(%http_addr, "starting HTTP reverse proxy server");
                let listener = tokio::net::TcpListener::bind(http_addr)
                    .await
                    .expect("failed to bind HTTP proxy server");
                
                tokio::select! {
                    _ = api_server => {
                        warn!("API server stopped unexpectedly");
                    }
                    result = axum::serve(listener, proxy_router) => {
                        if let Err(e) = result {
                            error!(error = %e, "HTTP proxy server error");
                        }
                    }
                }
            }
            // Only HTTPS
            (None, Some(https_addr), Some(cert), Some(key)) => {
                info!(%https_addr, "starting HTTPS reverse proxy server");
                match load_tls_config(&cert, &key, cert_store.clone()) {
                    Ok(tls_config) => {
                        tokio::select! {
                            _ = api_server => {
                                warn!("API server stopped unexpectedly");
                            }
                            result = axum_server::bind_rustls(https_addr, tls_config)
                                .serve(proxy_router.into_make_service()) => {
                                if let Err(e) = result {
                                    error!(error = %e, "HTTPS proxy server error");
                                }
                            }
                        }
                    }
                    Err(e) => {
                        error!(error = %e, "failed to load TLS config, proxy server not started");
                        api_server.await.ok();
                    }
                }
            }
            // Both HTTP and HTTPS
            (Some(http_addr), Some(https_addr), Some(cert), Some(key)) => {
                info!(%http_addr, %https_addr, "starting HTTP and HTTPS reverse proxy servers");
                
                let http_listener = tokio::net::TcpListener::bind(http_addr)
                    .await
                    .expect("failed to bind HTTP proxy server");
                
                let http_router = proxy_router.clone();
                let http_server = tokio::spawn(async move {
                    axum::serve(http_listener, http_router).await.expect("HTTP proxy error");
                });

                match load_tls_config(&cert, &key, cert_store.clone()) {
                    Ok(tls_config) => {
                        tokio::select! {
                            _ = api_server => {
                                warn!("API server stopped unexpectedly");
                            }
                            _ = http_server => {
                                warn!("HTTP proxy server stopped unexpectedly");
                            }
                            result = axum_server::bind_rustls(https_addr, tls_config)
                                .serve(proxy_router.into_make_service()) => {
                                if let Err(e) = result {
                                    error!(error = %e, "HTTPS proxy server error");
                                }
                            }
                        }
                    }
                    Err(e) => {
                        error!(error = %e, "failed to load TLS config, running HTTP only");
                        tokio::select! {
                            _ = api_server => {
                                warn!("API server stopped unexpectedly");
                            }
                            _ = http_server => {
                                warn!("HTTP proxy server stopped unexpectedly");
                            }
                        }
                    }
                }
            }
            _ => {
                // Check if we have an HTTPS address configured but no default cert
                // In that case, use SNI-only mode with per-domain certs
                let has_domain_certs = cert_store.read().map(|c| !c.is_empty()).unwrap_or(false);
                let https_addr_for_sni: Option<SocketAddr> = env::var("PROXY_HTTPS_ADDR")
                    .ok()
                    .and_then(|s| s.parse().ok());

                if has_domain_certs && https_addr_for_sni.is_some() {
                    let https_addr = https_addr_for_sni.unwrap();
                    info!(%https_addr, "starting HTTPS reverse proxy with SNI-only certs (no default cert)");
                    let tls_config = load_tls_config_sni_only(cert_store.clone());

                    let http_addr: SocketAddr = "0.0.0.0:80".parse().unwrap();
                    let http_listener = tokio::net::TcpListener::bind(http_addr)
                        .await
                        .expect("failed to bind HTTP proxy server");
                    let http_router = proxy_router.clone();
                    let http_server = tokio::spawn(async move {
                        axum::serve(http_listener, http_router).await.expect("HTTP proxy error");
                    });

                    tokio::select! {
                        _ = api_server => {
                            warn!("API server stopped unexpectedly");
                        }
                        _ = http_server => {
                            warn!("HTTP proxy server stopped unexpectedly");
                        }
                        result = axum_server::bind_rustls(https_addr, tls_config)
                            .serve(proxy_router.into_make_service()) => {
                            if let Err(e) = result {
                                error!(error = %e, "HTTPS proxy server error");
                            }
                        }
                    }
                } else {
                    warn!("no valid proxy TLS configuration found, falling back to HTTP on port 80");
                    let http_addr: SocketAddr = "0.0.0.0:80".parse().unwrap();
                    info!(%http_addr, "starting HTTP reverse proxy server (fallback)");
                    let listener = tokio::net::TcpListener::bind(http_addr)
                        .await
                        .expect("failed to bind HTTP proxy server");
                    
                    tokio::select! {
                        _ = api_server => {
                            warn!("API server stopped unexpectedly");
                        }
                        result = axum::serve(listener, proxy_router) => {
                            if let Err(e) = result {
                                error!(error = %e, "HTTP proxy server error");
                            }
                        }
                    }
                }
            }
        }
    }
}

/// Load TLS configuration from certificate and key files with SNI-based cert resolution
fn load_tls_config(
    cert_path: &str,
    key_path: &str,
    cert_store: proxy::CertStore,
) -> Result<axum_server::tls_rustls::RustlsConfig, Box<dyn std::error::Error>> {
    use rustls_pemfile::{certs, private_key};
    use std::io::Cursor;
    use tokio_rustls::rustls::{self, pki_types::CertificateDer};

    // Check if files exist
    if !Path::new(cert_path).exists() {
        return Err(format!("certificate file not found: {}", cert_path).into());
    }
    if !Path::new(key_path).exists() {
        return Err(format!("key file not found: {}", key_path).into());
    }

    // Read certificate file
    let cert_file = fs::read(cert_path)
        .map_err(|e| format!("failed to read cert file: {}", e))?;
    let mut cert_reader = BufReader::new(Cursor::new(cert_file));
    let cert_chain: Vec<CertificateDer> = certs(&mut cert_reader)
        .collect::<Result<_, _>>()
        .map_err(|e| format!("failed to parse certificates: {}", e))?;

    if cert_chain.is_empty() {
        return Err("no certificates found in cert file".into());
    }

    // Read private key file
    let key_file = fs::read(key_path)
        .map_err(|e| format!("failed to read key file: {}", e))?;
    let mut key_reader = BufReader::new(Cursor::new(key_file));
    let key_der = private_key(&mut key_reader)
        .map_err(|e| format!("failed to parse private key: {}", e))?
        .ok_or("no private keys found in key file")?;

    // Build default CertifiedKey
    let provider = rustls::crypto::ring::default_provider();
    let signing_key = provider
        .key_provider
        .load_private_key(key_der)
        .map_err(|e| format!("failed to load signing key: {}", e))?;
    let default_cert = Arc::new(rustls::sign::CertifiedKey::new(cert_chain, signing_key));

    // Build TLS config with SNI-based cert resolver
    let resolver = proxy::DomainCertResolver {
        default_cert: Some(default_cert),
        domain_certs: cert_store,
    };

    let mut config = rustls::ServerConfig::builder()
        .with_no_client_auth()
        .with_cert_resolver(Arc::new(resolver));

    config.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];

    Ok(axum_server::tls_rustls::RustlsConfig::from_config(Arc::new(config)))
}

/// Load TLS configuration with only per-domain certificates (no default cert)
fn load_tls_config_sni_only(
    cert_store: proxy::CertStore,
) -> axum_server::tls_rustls::RustlsConfig {
    use tokio_rustls::rustls;

    let resolver = proxy::DomainCertResolver {
        default_cert: None,
        domain_certs: cert_store,
    };

    let mut config = rustls::ServerConfig::builder()
        .with_no_client_auth()
        .with_cert_resolver(Arc::new(resolver));

    config.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];

    axum_server::tls_rustls::RustlsConfig::from_config(Arc::new(config))
}

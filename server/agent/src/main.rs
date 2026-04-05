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
mod resource;

use app_state::AppState;
use axum::{Router, middleware, routing::{get, post}};
use collector::start_collector;
use db::init_db;
use docs::ApiDoc;
use rusqlite::Connection;
use std::{env, net::SocketAddr, sync::Arc};
use tokio::sync::Mutex;
use tracing::{info, warn};
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

    info!(
        traffic_collect_interval,
        resource_collect_interval,
        %traffic_collect_method,
        "collection intervals configured"
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

    let state = AppState {
        conn: Arc::new(Mutex::new(conn)),
        api_token,
        traffic_collect_interval,
        resource_collect_interval,
        traffic_collect_method,
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

    let addr: SocketAddr = "0.0.0.0:23782".parse().expect("invalid bind address");
    info!(%addr, "starting http server");
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .expect("failed to bind server");
    axum::serve(listener, app).await.expect("server error");
}

use rusqlite::Connection;
use std::sync::Arc;
use tokio::sync::Mutex;

use crate::proxy::ProxyRoutes;

#[derive(Clone)]
pub struct AppState {
    pub conn: Arc<Mutex<Connection>>,
    pub api_token: String,
    /// Traffic collection interval in seconds (default: 5)
    pub traffic_collect_interval: u64,
    /// Resource collection interval in seconds (default: 30)
    pub resource_collect_interval: u64,
    /// Traffic collection method: "nft" (default) or "ipt"
    pub traffic_collect_method: String,
    /// Proxy routes for domain reverse proxy
    pub proxy_routes: ProxyRoutes,
}

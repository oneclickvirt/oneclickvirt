use rusqlite::Connection;
use std::sync::Arc;
use tokio::sync::Mutex;

use crate::proxy::{CertStore, ProxyRoutes};

#[derive(Clone)]
pub struct AppState {
    pub conn: Arc<Mutex<Connection>>,
    /// Serializes nftables/iptables mutations and counter snapshots without holding SQLite locks.
    pub traffic_operation_lock: Arc<Mutex<()>>,
    pub api_token: String,
    /// Traffic collection interval in seconds (default: 5)
    pub traffic_collect_interval: u64,
    /// Maximum active interface counters read in one traffic collection tick.
    pub traffic_collect_batch_size: usize,
    /// Desired-state reconciliation interval in seconds (default: 60)
    pub traffic_reconcile_interval: u64,
    /// Maximum monitors reconciled in one desired-state pass.
    pub traffic_reconcile_batch_size: usize,
    /// Resource collection interval in seconds (default: 30)
    pub resource_collect_interval: u64,
    /// Maximum resource monitors probed in one collection pass.
    pub resource_collect_batch_size: usize,
    /// Traffic collection method: "nft" (default) or "ipt"
    pub traffic_collect_method: String,
    /// Proxy routes for domain reverse proxy
    pub proxy_routes: ProxyRoutes,
    /// Per-domain TLS certificate store
    pub cert_store: CertStore,
}

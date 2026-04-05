use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

#[derive(Deserialize, ToSchema)]
pub struct AddRequest {
    pub interface: InterfaceInput,
    /// Provider type hint for resource monitoring: docker/podman/containerd/lxd/incus/proxmox
    pub provider_kind: Option<String>,
    /// Instance name on the provider host (container/VM name or VMID for proxmox)
    pub instance_name: Option<String>,
    /// Inner IP address of the instance (e.g. 172.17.0.3) for per-IP traffic filtering
    pub inner_ip: Option<String>,
}

#[derive(Deserialize, ToSchema)]
pub struct UpdateRequest {
    pub id: i64,
    pub new_interface: InterfaceInput,
    pub provider_kind: Option<String>,
    pub instance_name: Option<String>,
    /// Inner IP address of the instance for per-IP traffic filtering
    pub inner_ip: Option<String>,
}

#[derive(Deserialize, ToSchema)]
pub struct DeleteRequest {
    pub id: i64,
}

#[derive(Deserialize, ToSchema)]
pub struct InfoRequest {
    pub id: i64,
}

#[derive(Deserialize, ToSchema)]
pub struct CleanupRequest {
    pub max_update_time: String,
}

#[derive(Deserialize, ToSchema)]
pub struct ResourceQueryRequest {
    pub id: i64,
    /// Max number of data points to return (default 288 = 24h at 5min interval)
    pub limit: Option<i64>,
}

#[derive(Deserialize, ToSchema)]
#[serde(untagged)]
pub enum InterfaceInput {
    One(String),
    Many(Vec<String>),
}

impl InterfaceInput {
    pub fn into_vec(self) -> Vec<String> {
        match self {
            Self::One(one) => vec![one],
            Self::Many(many) => many,
        }
    }
}

#[derive(Serialize, ToSchema)]
pub struct AddResponse {
    pub id: i64,
    pub interface: Vec<String>,
}

#[derive(Serialize, ToSchema)]
pub struct UpdateResponse {
    pub id: i64,
    pub interface: Vec<String>,
}

#[derive(Serialize, ToSchema)]
pub struct DeleteResponse {
    pub id: i64,
    pub deleted: bool,
}

#[derive(Serialize, ToSchema)]
pub struct InfoResponse {
    pub id: i64,
    pub interface: Vec<String>,
    pub used_traffic: u64,
    pub used_traffic_in: u64,
    pub used_traffic_out: u64,
    pub used_traffic_human: Option<String>,
    pub last_update_time: i64,
}

#[derive(Serialize, ToSchema)]
pub struct CleanupResponse {
    pub deleted: usize,
    pub max_update_seconds: i64,
}

#[derive(Serialize, ToSchema)]
pub struct ResourceDataPoint {
    pub timestamp: i64,
    pub cpu_percent: f64,
    pub memory_used: u64,
    pub memory_total: u64,
    pub disk_used: u64,
    pub disk_total: u64,
}

#[derive(Serialize, ToSchema)]
pub struct ResourceQueryResponse {
    pub id: i64,
    pub data: Vec<ResourceDataPoint>,
}

#[derive(Serialize, ToSchema)]
pub struct ListMonitorItem {
    pub id: i64,
    pub interface: Vec<String>,
    pub provider_kind: Option<String>,
    pub instance_name: Option<String>,
    pub total_bytes: u64,
    pub updated_at: i64,
}

#[derive(Serialize, ToSchema)]
pub struct ListMonitorsResponse {
    pub monitors: Vec<ListMonitorItem>,
    pub total: usize,
}

// ---- Block Rules ----

#[derive(Deserialize, ToSchema)]
pub struct ApplyBlockRulesRequest {
    pub strings: Vec<String>,
    /// IP version filter: "both" (default), "ipv4", "ipv6"
    pub ip_version: Option<String>,
}

#[derive(Serialize, ToSchema)]
pub struct ApplyBlockRulesResponse {
    pub applied: usize,
}

#[derive(Serialize, ToSchema)]
pub struct RemoveBlockRulesResponse {
    pub removed: bool,
}

#[derive(Serialize, ToSchema)]
pub struct GetBlockRulesResponse {
    pub strings: Vec<String>,
    pub count: usize,
    pub ip_version: String,
}

// ---- Domain Proxy ----

#[derive(Deserialize, ToSchema)]
pub struct AddDomainProxyRequest {
    /// Fully qualified domain name (e.g. app.example.com)
    pub domain: String,
    /// Internal target IP (e.g. 172.17.0.3)
    pub internal_ip: String,
    /// Internal target port (e.g. 8080)
    pub internal_port: u16,
    /// Protocol: http or https (default http)
    pub protocol: Option<String>,
    /// Enable SSL termination for this domain
    pub enable_ssl: Option<bool>,
    /// PEM-encoded SSL certificate (full chain)
    pub ssl_cert: Option<String>,
    /// PEM-encoded SSL private key
    pub ssl_key: Option<String>,
}

#[derive(Serialize, ToSchema)]
pub struct AddDomainProxyResponse {
    pub domain: String,
    pub status: String,
}

#[derive(Deserialize, ToSchema)]
pub struct RemoveDomainProxyRequest {
    /// Domain name to remove proxy for
    pub domain: String,
}

#[derive(Serialize, ToSchema)]
pub struct RemoveDomainProxyResponse {
    pub domain: String,
    pub removed: bool,
}

#[derive(Serialize, ToSchema)]
pub struct DomainProxyItem {
    pub domain: String,
    pub internal_ip: String,
    pub internal_port: u16,
    pub protocol: String,
    pub enable_ssl: bool,
    pub has_cert: bool,
    pub created_at: i64,
}

#[derive(Serialize, ToSchema)]
pub struct ListDomainProxiesResponse {
    pub proxies: Vec<DomainProxyItem>,
    pub total: usize,
}

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

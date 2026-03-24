use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

#[derive(Deserialize, ToSchema)]
pub struct AddRequest {
    pub interface: InterfaceInput,
}

#[derive(Deserialize, ToSchema)]
pub struct UpdateRequest {
    pub id: i64,
    pub new_interface: InterfaceInput,
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
    pub used_traffic_human: Option<String>,
    pub last_update_time: i64,
}

#[derive(Serialize, ToSchema)]
pub struct CleanupResponse {
    pub deleted: usize,
    pub max_update_seconds: i64,
}

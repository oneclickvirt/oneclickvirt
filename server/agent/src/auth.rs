use crate::{app_state::AppState, error::ApiError};
use axum::{
    extract::{Request, State},
    middleware::Next,
    response::Response,
};
use tracing::warn;

pub async fn require_token(
    State(state): State<AppState>,
    request: Request,
    next: Next,
) -> Result<Response, ApiError> {
    let path = request.uri().path().to_owned();
    let token = request
        .headers()
        .get("x-token")
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .filter(|v| !v.is_empty())
        .ok_or_else(|| {
            warn!(%path, "unauthorized request: missing x-token header");
            ApiError::unauthorized("missing x-token header")
        })?;

    if token != state.api_token {
        warn!(%path, "unauthorized request: invalid token");
        return Err(ApiError::unauthorized("invalid token"));
    }

    Ok(next.run(request).await)
}

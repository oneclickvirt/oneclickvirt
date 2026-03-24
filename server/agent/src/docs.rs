use utoipa::openapi::{
    ComponentsBuilder,
    security::{ApiKey, ApiKeyValue, SecurityRequirement, SecurityScheme},
};
use utoipa::{Modify, OpenApi};

pub struct SecurityAddon;

impl Modify for SecurityAddon {
    fn modify(&self, openapi: &mut utoipa::openapi::OpenApi) {
        let mut components = openapi
            .components
            .take()
            .unwrap_or_else(|| ComponentsBuilder::new().build());

        components.add_security_scheme(
            "token_auth",
            SecurityScheme::ApiKey(ApiKey::Header(ApiKeyValue::new("x-token"))),
        );

        openapi.components = Some(components);
        openapi.security = Some(vec![SecurityRequirement::new(
            "token_auth",
            Vec::<String>::new(),
        )]);
    }
}

#[derive(OpenApi)]
#[openapi(
    paths(
        crate::handlers::add_monitor,
        crate::handlers::update_monitor,
        crate::handlers::delete_monitor,
        crate::handlers::info_monitor,
        crate::handlers::cleanup_monitor
    ),
    components(
        schemas(
            crate::models::AddRequest,
            crate::models::UpdateRequest,
            crate::models::DeleteRequest,
            crate::models::InfoRequest,
            crate::models::CleanupRequest,
            crate::models::InterfaceInput,
            crate::models::AddResponse,
            crate::models::UpdateResponse,
            crate::models::DeleteResponse,
            crate::models::InfoResponse,
            crate::models::CleanupResponse,
            crate::error::ErrorResponse
        )
    ),
    modifiers(&SecurityAddon),
    tags(
        (name = "VM Traffic", description = "VM traffic monitor APIs")
    )
)]
pub struct ApiDoc;

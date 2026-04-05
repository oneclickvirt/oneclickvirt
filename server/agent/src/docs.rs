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
        crate::handlers::cleanup_monitor,
        crate::handlers::query_resources,
        crate::handlers::list_monitors,
        crate::handlers::apply_block_rules,
        crate::handlers::remove_block_rules,
        crate::handlers::get_block_rules,
        crate::handlers::add_domain_proxy,
        crate::handlers::remove_domain_proxy,
        crate::handlers::list_domain_proxies
    ),
    components(
        schemas(
            crate::models::AddRequest,
            crate::models::UpdateRequest,
            crate::models::DeleteRequest,
            crate::models::InfoRequest,
            crate::models::CleanupRequest,
            crate::models::ResourceQueryRequest,
            crate::models::InterfaceInput,
            crate::models::AddResponse,
            crate::models::UpdateResponse,
            crate::models::DeleteResponse,
            crate::models::InfoResponse,
            crate::models::CleanupResponse,
            crate::models::ResourceDataPoint,
            crate::models::ResourceQueryResponse,
            crate::models::ListMonitorItem,
            crate::models::ListMonitorsResponse,
            crate::models::ApplyBlockRulesRequest,
            crate::models::ApplyBlockRulesResponse,
            crate::models::RemoveBlockRulesResponse,
            crate::models::GetBlockRulesResponse,
            crate::models::AddDomainProxyRequest,
            crate::models::AddDomainProxyResponse,
            crate::models::RemoveDomainProxyRequest,
            crate::models::RemoveDomainProxyResponse,
            crate::models::DomainProxyItem,
            crate::models::ListDomainProxiesResponse,
            crate::resource::ResourceSnapshot,
            crate::resource::ProviderKind,
            crate::error::ErrorResponse
        )
    ),
    modifiers(&SecurityAddon),
    tags(
        (name = "VM Traffic", description = "VM traffic monitor APIs"),
        (name = "Resource Monitoring", description = "Instance resource monitoring APIs"),
        (name = "Block Rules", description = "Network block rule management APIs"),
        (name = "Domain Proxy", description = "Domain reverse proxy management APIs")
    )
)]
pub struct ApiDoc;

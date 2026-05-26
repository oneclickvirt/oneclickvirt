// Barrel file — re-exports all admin API functions for backward compatibility.
// Domain-specific implementations live in ./admin/*.js
export * from './admin/tasks'
export * from './admin/monitoring'
export * from './admin/dashboard'
export * from './admin/users'
export * from './admin/providers'
export * from './admin/instances'
export * from './admin/config'
export * from './admin/content'
export * from './admin/port-mappings'
export * from './admin/traffic'

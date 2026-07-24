//! Host-native, per-instance transparent egress routing.
//!
//! The implementation is split by responsibility while remaining in this
//! module so validation, persistence, and reconciliation share private types.

include!("egress_parts/types_db.rs");
include!("egress_parts/dependencies.rs");
include!("egress_parts/storage.rs");
include!("egress_parts/persistence.rs");
include!("egress_parts/handlers.rs");
include!("egress_parts/planning.rs");
include!("egress_parts/runtime.rs");
include!("egress_parts/reconcile.rs");
include!("egress_parts/tests.rs");

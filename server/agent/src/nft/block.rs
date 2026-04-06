use crate::error::ApiError;
use std::{fs, path::Path};
use tracing::{info, warn};

use super::{run_nft, run_nft_script};

const BLOCK_TABLE: &str = "abuse_block";
const BLOCK_FAMILY: &str = "inet";
const BLOCK_CHAIN_OUTPUT: &str = "block_output";
const BLOCK_CHAIN_FORWARD: &str = "block_forward";
const BLOCK_RULES_FILE: &str = "/opt/oneclickvirt/agent/block_rules.json";

/// Ensure the abuse_block nft table and chains exist.
fn ensure_block_table() -> Result<(), ApiError> {
    let script = format!(
        "add table {BLOCK_FAMILY} {BLOCK_TABLE}\n\
         add chain {BLOCK_FAMILY} {BLOCK_TABLE} {BLOCK_CHAIN_OUTPUT} {{ type filter hook output priority 0; policy accept; }}\n\
         add chain {BLOCK_FAMILY} {BLOCK_TABLE} {BLOCK_CHAIN_FORWARD} {{ type filter hook forward priority 0; policy accept; }}\n"
    );
    run_nft_script(&script)
}

/// Flush all rules in the block chains.
fn flush_block_chains() -> Result<(), ApiError> {
    let script = format!(
        "flush chain {BLOCK_FAMILY} {BLOCK_TABLE} {BLOCK_CHAIN_OUTPUT}\n\
         flush chain {BLOCK_FAMILY} {BLOCK_TABLE} {BLOCK_CHAIN_FORWARD}\n"
    );
    run_nft_script(&script)
}

/// Remove the entire abuse_block table.
fn remove_block_table() {
    let _ = run_nft(&["delete", "table", BLOCK_FAMILY, BLOCK_TABLE]);
}

/// Persisted block rules state including ip_version
#[derive(serde::Serialize, serde::Deserialize)]
struct PersistedBlockRules {
    strings: Vec<String>,
    ip_version: String,
}

/// nft raw payload `@th` match supports a maximum of 128 bits (16 bytes) per
/// expression on most kernels. For strings longer than 16 bytes we split them
/// into consecutive 16-byte chunks, each at the correct bit offset, and combine
/// them into a single rule so that nft can evaluate the expression chain.
const NFT_MAX_MATCH_BYTES: usize = 16;

/// Build a single nft match expression for `pattern_bytes` starting at
/// `base_offset_bits` inside the transport header. Long patterns are split
/// into consecutive chunks of at most `NFT_MAX_MATCH_BYTES`.
fn build_nft_payload_match(base_offset_bits: usize, pattern_bytes: &[u8]) -> String {
    let mut parts: Vec<String> = Vec::new();
    let mut pos = 0usize;
    while pos < pattern_bytes.len() {
        let remaining = pattern_bytes.len() - pos;
        let chunk_len = remaining.min(NFT_MAX_MATCH_BYTES);
        let bit_offset = base_offset_bits + pos * 8;
        let bit_len = chunk_len * 8;
        let hex_str: String = pattern_bytes[pos..pos + chunk_len]
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        parts.push(format!("@th,{bit_offset},{bit_len} 0x{hex_str}"));
        pos += chunk_len;
    }
    parts.join(" ")
}

/// Apply string-match block rules using nft.
/// Uses nft raw payload matching at the start of transport payload.
/// For TCP: matches at offset 160 bits (20-byte header, no options).
/// For UDP: matches at offset 64 bits (8-byte header).
/// Long strings are automatically split into chained 16-byte chunks to stay
/// within the kernel's per-expression limit.
/// ip_version: "both" (default), "ipv4", "ipv6"
pub fn apply_block_rules(strings: &[String], ip_version: &str) -> Result<usize, ApiError> {
    if strings.is_empty() {
        return Ok(0);
    }

    ensure_block_table()?;
    flush_block_chains()?;

    let mut script = String::new();
    for s in strings {
        if s.is_empty() { continue; }
        // Hard cap at 128 bytes (reasonable upper bound for content patterns)
        if s.len() > 128 { continue; }

        let pattern = s.as_bytes();

        // Build match expressions for the relevant IP versions
        let family_filter = match ip_version {
            "ipv4" => "meta nfproto ipv4 ",
            "ipv6" => "meta nfproto ipv6 ",
            _ => "", // "both" - no filter
        };

        // TCP payload offset: 160 bits (20-byte header)
        let tcp_match = build_nft_payload_match(160, pattern);
        // UDP payload offset: 64 bits (8-byte header)
        let udp_match = build_nft_payload_match(64, pattern);

        for chain in [BLOCK_CHAIN_OUTPUT, BLOCK_CHAIN_FORWARD] {
            script.push_str(&format!(
                "add rule {BLOCK_FAMILY} {BLOCK_TABLE} {chain} {family_filter}meta l4proto tcp {tcp_match} counter drop\n"
            ));
            script.push_str(&format!(
                "add rule {BLOCK_FAMILY} {BLOCK_TABLE} {chain} {family_filter}meta l4proto udp {udp_match} counter drop\n"
            ));
        }
    }

    if !script.is_empty() {
        run_nft_script(&script)?;
    }

    // Persist for restart recovery
    let persisted = PersistedBlockRules {
        strings: strings.to_vec(),
        ip_version: ip_version.to_string(),
    };
    if let Ok(json) = serde_json::to_string(&persisted) {
        let _ = fs::write(Path::new(BLOCK_RULES_FILE), json);
    }

    let count = strings.iter().filter(|s| !s.is_empty()).count();
    info!(count, ip_version, "applied abuse block rules via nft");
    Ok(count)
}

/// Remove all block rules.
pub fn remove_block_rules() -> Result<(), ApiError> {
    remove_block_table();
    let _ = fs::remove_file(Path::new(BLOCK_RULES_FILE));
    info!("removed abuse block rules");
    Ok(())
}

/// Get current block rules from the persisted file.
pub fn get_block_rules() -> (Vec<String>, String) {
    let block_file = Path::new(BLOCK_RULES_FILE);
    if let Ok(content) = fs::read_to_string(block_file) {
        // Try new format first
        if let Ok(persisted) = serde_json::from_str::<PersistedBlockRules>(&content) {
            return (persisted.strings, persisted.ip_version);
        }
        // Fallback: old format (plain array)
        if let Ok(strings) = serde_json::from_str::<Vec<String>>(&content) {
            return (strings, "both".to_string());
        }
    }
    (Vec::new(), "both".to_string())
}

/// Restore block rules from persisted file on startup.
pub fn restore_block_rules() {
    let (strings, ip_version) = get_block_rules();
    if strings.is_empty() {
        return;
    }
    match apply_block_rules(&strings, &ip_version) {
        Ok(count) => info!(count, ip_version, "restored persisted block rules on startup"),
        Err(e) => warn!(error = %e.message, "failed to restore block rules on startup"),
    }
}

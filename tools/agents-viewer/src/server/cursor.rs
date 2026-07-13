use base64::Engine as _;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use super::ApiFailure;

const MAX_CURSOR_BYTES: usize = 2_048;

#[derive(Clone, Debug, Deserialize, Serialize)]
struct Cursor {
    v: u8,
    endpoint: String,
    filter_hash: String,
    sort: i64,
    id: String,
    direction: String,
}

pub fn encode(
    endpoint: &str,
    canonical_filters: &str,
    sort: i64,
    id: &str,
    direction: &str,
) -> String {
    let cursor = Cursor {
        v: 1,
        endpoint: endpoint.into(),
        filter_hash: filter_hash(canonical_filters),
        sort,
        id: id.into(),
        direction: direction.into(),
    };
    base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(serde_json::to_vec(&cursor).expect("cursor has a fixed serializable schema"))
}

pub fn decode(
    value: &str,
    endpoint: &str,
    canonical_filters: &str,
) -> Result<(i64, String, String), ApiFailure> {
    if value.len() > MAX_CURSOR_BYTES {
        return Err(ApiFailure::invalid("cursor is too long"));
    }
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| ApiFailure::invalid("cursor is not valid base64url"))?;
    let cursor: Cursor = serde_json::from_slice(&bytes)
        .map_err(|_| ApiFailure::invalid("cursor has an invalid schema"))?;
    if cursor.v != 1
        || cursor.endpoint != endpoint
        || cursor.filter_hash != filter_hash(canonical_filters)
        || cursor.id.is_empty()
        || cursor.id.len() > 512
        || !matches!(cursor.direction.as_str(), "next" | "previous")
    {
        return Err(ApiFailure::invalid(
            "cursor does not match this endpoint or filter set",
        ));
    }
    Ok((cursor.sort, cursor.id, cursor.direction))
}

fn filter_hash(value: &str) -> String {
    let digest = Sha256::digest(value.as_bytes());
    digest[..16]
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

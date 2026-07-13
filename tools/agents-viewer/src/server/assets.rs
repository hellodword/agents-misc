use axum::body::Body;
use axum::extract::Path;
use axum::response::Response;
use http::header::{CACHE_CONTROL, CONTENT_TYPE};
use http::{HeaderValue, StatusCode};

use super::ApiFailure;

const STUB_HTML: &[u8] = b"<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Agents Viewer</title></head><body><main id=\"root\">Agents Viewer</main></body></html>";

#[cfg(feature = "embedded-ui")]
static WEB_DIST: include_dir::Dir<'_> = include_dir::include_dir!("$CARGO_MANIFEST_DIR/web/dist");

pub async fn root() -> Response {
    asset_response("index.html", false)
}

pub async fn fallback(Path(path): Path<String>) -> Result<Response, ApiFailure> {
    if path.starts_with("api/") {
        return Err(ApiFailure::not_found("API endpoint does not exist"));
    }
    #[cfg(feature = "embedded-ui")]
    if WEB_DIST.get_file(&path).is_some() {
        return Ok(asset_response(&path, true));
    }
    Ok(asset_response("index.html", false))
}

fn asset_response(path: &str, asset: bool) -> Response {
    #[cfg(feature = "embedded-ui")]
    let bytes = WEB_DIST
        .get_file(path)
        .map_or(STUB_HTML, include_dir::File::contents);
    #[cfg(not(feature = "embedded-ui"))]
    let bytes = {
        let _ = path;
        STUB_HTML
    };
    let mime = if path == "index.html" {
        "text/html; charset=utf-8".to_owned()
    } else {
        mime_guess::from_path(path)
            .first_or_octet_stream()
            .to_string()
    };
    let cache = if asset && is_hashed_asset(path) {
        "public, max-age=31536000, immutable"
    } else {
        "no-cache"
    };
    Response::builder()
        .status(StatusCode::OK)
        .header(
            CONTENT_TYPE,
            HeaderValue::from_str(&mime).expect("valid MIME"),
        )
        .header(CACHE_CONTROL, cache)
        .body(Body::from(bytes))
        .expect("fixed asset response")
}

fn is_hashed_asset(path: &str) -> bool {
    path.split('.').any(|part| {
        part.len() >= 8
            && part.len() <= 64
            && part.chars().all(|character| character.is_ascii_hexdigit())
    })
}

use std::net::SocketAddr;

use axum::extract::{Request, State};
use axum::middleware::Next;
use axum::response::{IntoResponse as _, Response};
use base64::Engine as _;
use http::header::{
    AUTHORIZATION, CACHE_CONTROL, CONTENT_LENGTH, CONTENT_SECURITY_POLICY, HOST, ORIGIN,
    REFERRER_POLICY, WWW_AUTHENTICATE, X_CONTENT_TYPE_OPTIONS, X_FRAME_OPTIONS,
};
use http::{HeaderName, HeaderValue, Method, StatusCode, Uri};
use sha2::{Digest as _, Sha256};

use super::ApiFailure;

const CSP: &str = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'";
const BASIC_AUTH_USERNAME: &str = "agents-viewer";
const BASIC_AUTH_CHALLENGE: &str = "Basic realm=\"agents-viewer\", charset=\"UTF-8\"";
const PASSWORD_TAG_DOMAIN: &[u8] = b"agents-viewer/basic-auth/password\0";

#[derive(Clone)]
pub struct SecurityConfig {
    allowed_authorities: Vec<String>,
    password_tag: Option<[u8; 32]>,
}

impl SecurityConfig {
    #[must_use]
    pub fn new(bound: SocketAddr, password: &str) -> Self {
        let port = bound.port();
        let mut allowed_authorities = vec![bound.to_string(), format!("localhost:{port}")];
        allowed_authorities.sort();
        allowed_authorities.dedup();
        Self {
            allowed_authorities,
            password_tag: (!password.is_empty()).then(|| password_tag(password)),
        }
    }

    fn allows(&self, authority: &str) -> bool {
        self.allowed_authorities
            .iter()
            .any(|allowed| allowed.eq_ignore_ascii_case(authority))
    }

    fn authenticates(&self, request: &Request) -> bool {
        let Some(expected) = self.password_tag.as_ref() else {
            return true;
        };
        let Some(value) = request
            .headers()
            .get(AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
        else {
            return false;
        };
        let Some((scheme, encoded)) = value.split_once(' ') else {
            return false;
        };
        if !scheme.eq_ignore_ascii_case("basic") || encoded.is_empty() {
            return false;
        }
        let Ok(decoded) = base64::engine::general_purpose::STANDARD.decode(encoded) else {
            return false;
        };
        let Ok(credentials) = std::str::from_utf8(&decoded) else {
            return false;
        };
        let Some((username, password)) = credentials.split_once(':') else {
            return false;
        };
        username == BASIC_AUTH_USERNAME && password_matches(password, expected)
    }
}

pub async fn secure_request(
    State(config): State<SecurityConfig>,
    request: Request,
    next: Next,
) -> Response {
    let path = request.uri().path().to_owned();
    let mut response = match validate_request(&config, &request) {
        Ok(()) if config.authenticates(&request) => next.run(request).await,
        Ok(()) => authentication_required(),
        Err(error) => error.into_response(),
    };
    apply_security_headers(&mut response, &path);
    response
}

fn password_tag(password: &str) -> [u8; 32] {
    let mut digest = Sha256::new();
    digest.update(PASSWORD_TAG_DOMAIN);
    digest.update(password.as_bytes());
    digest.finalize().into()
}

fn password_matches(password: &str, expected: &[u8; 32]) -> bool {
    let candidate = password_tag(password);
    let difference = candidate
        .iter()
        .zip(expected)
        .fold(0_u8, |difference, (left, right)| {
            difference | (left ^ right)
        });
    std::hint::black_box(difference) == 0
}

fn authentication_required() -> Response {
    let mut response = ApiFailure::new(
        StatusCode::UNAUTHORIZED,
        "authentication_required",
        "HTTP Basic authentication is required",
    )
    .into_response();
    response.headers_mut().insert(
        WWW_AUTHENTICATE,
        HeaderValue::from_static(BASIC_AUTH_CHALLENGE),
    );
    response
        .headers_mut()
        .insert(CACHE_CONTROL, HeaderValue::from_static("no-store"));
    response
}

fn validate_request(config: &SecurityConfig, request: &Request) -> Result<(), ApiFailure> {
    let host = request
        .headers()
        .get(HOST)
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| ApiFailure::forbidden("missing or invalid Host header"))?;
    if !config.allows(host) {
        return Err(ApiFailure::forbidden(
            "Host is not the bound loopback service",
        ));
    }
    if let Some(origin) = request.headers().get(ORIGIN) {
        let origin = origin
            .to_str()
            .map_err(|_| ApiFailure::forbidden("invalid Origin header"))?;
        if origin == "null" || !valid_origin(origin, config) {
            return Err(ApiFailure::forbidden(
                "Origin is not the bound loopback service",
            ));
        }
    }
    if request
        .headers()
        .contains_key(http::header::TRANSFER_ENCODING)
        || request
            .headers()
            .get(CONTENT_LENGTH)
            .and_then(|value| value.to_str().ok())
            .is_some_and(|value| value != "0")
    {
        return Err(ApiFailure::invalid("request bodies are not accepted"));
    }
    if request.method() != Method::GET && request.method() != Method::HEAD {
        return Err(ApiFailure::new(
            StatusCode::METHOD_NOT_ALLOWED,
            "invalid_argument",
            "only GET requests are accepted",
        ));
    }
    Ok(())
}

fn apply_security_headers(response: &mut Response, path: &str) {
    let headers = response.headers_mut();
    headers.insert(CONTENT_SECURITY_POLICY, HeaderValue::from_static(CSP));
    headers.insert(X_CONTENT_TYPE_OPTIONS, HeaderValue::from_static("nosniff"));
    headers.insert(REFERRER_POLICY, HeaderValue::from_static("no-referrer"));
    headers.insert(
        HeaderName::from_static("cross-origin-resource-policy"),
        HeaderValue::from_static("same-origin"),
    );
    headers.insert(X_FRAME_OPTIONS, HeaderValue::from_static("DENY"));
    if path.starts_with("/api/") {
        headers.insert(CACHE_CONTROL, HeaderValue::from_static("no-store"));
    }
}

fn valid_origin(origin: &str, config: &SecurityConfig) -> bool {
    let Ok(uri) = origin.parse::<Uri>() else {
        return false;
    };
    matches!(uri.scheme_str(), Some("http" | "https"))
        && uri
            .authority()
            .is_some_and(|authority| config.allows(authority.as_str()))
        && uri.path() == "/"
        && uri.query().is_none()
}

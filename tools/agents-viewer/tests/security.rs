mod support;

use base64::Engine as _;
use http::{Method, StatusCode};
use tower::ServiceExt as _;

#[tokio::test]
async fn host_origin_body_method_and_security_headers_are_enforced() {
    let app = support::TestApp::new().await;
    let router = app.router();
    for (header, value) in [
        ("host", "evil.example:4747"),
        ("origin", "https://evil.example"),
    ] {
        let mut request = support::request("/api/v1/status");
        request.headers_mut().insert(header, value.parse().unwrap());
        let response = router.clone().oneshot(request).await.unwrap();
        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        assert!(
            response
                .headers()
                .get("access-control-allow-origin")
                .is_none()
        );
        assert!(response.headers().contains_key("content-security-policy"));
    }
    let mut request = support::request("/api/v1/status");
    request
        .headers_mut()
        .insert("origin", "null".parse().unwrap());
    assert_eq!(
        router.clone().oneshot(request).await.unwrap().status(),
        StatusCode::FORBIDDEN
    );
    let mut request = support::request("/api/v1/status");
    *request.method_mut() = Method::POST;
    assert_eq!(
        router.clone().oneshot(request).await.unwrap().status(),
        StatusCode::METHOD_NOT_ALLOWED
    );
    let mut request = support::request("/api/v1/status");
    request
        .headers_mut()
        .insert("content-length", "1".parse().unwrap());
    assert_eq!(
        router.clone().oneshot(request).await.unwrap().status(),
        StatusCode::BAD_REQUEST
    );

    let response = router
        .clone()
        .oneshot(support::request("/api/v1/status"))
        .await
        .unwrap();
    for header in [
        "content-security-policy",
        "x-content-type-options",
        "referrer-policy",
        "cross-origin-resource-policy",
        "x-frame-options",
    ] {
        assert!(response.headers().contains_key(header), "missing {header}");
    }
    assert!(response.headers().get("set-cookie").is_none());
    assert!(
        response
            .headers()
            .get("access-control-allow-origin")
            .is_none()
    );
    let mut same_origin = support::request("/api/v1/status");
    same_origin
        .headers_mut()
        .insert("origin", "http://localhost:4747".parse().unwrap());
    assert_eq!(
        router.clone().oneshot(same_origin).await.unwrap().status(),
        StatusCode::OK
    );
    let response = router
        .oneshot(support::request("/sessions/deep-link"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["cache-control"], "no-cache");
}

#[tokio::test]
async fn basic_auth_protects_assets_api_raw_content_and_events() {
    let app = support::TestApp::new().await;
    let router = app.router_with_password("correct horse:电池订书钉");

    for path in [
        "/",
        "/sessions/deep-link",
        "/api/v1/status",
        "/api/v1/sessions",
        "/api/v1/sessions/session/raw",
        "/api/v1/content/token",
        "/api/v1/events",
    ] {
        let response = router
            .clone()
            .oneshot(support::request(path))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{path}");
        assert_eq!(
            response.headers()["www-authenticate"],
            "Basic realm=\"agents-viewer\", charset=\"UTF-8\""
        );
        assert_eq!(response.headers()["cache-control"], "no-store");
        assert!(response.headers().get("set-cookie").is_none());
    }

    for authorization in [
        "Bearer token".to_owned(),
        "Basic !!!".to_owned(),
        basic_authorization("someone-else", "correct horse:电池订书钉"),
        basic_authorization("agents-viewer", "wrong"),
    ] {
        let mut request = support::request("/api/v1/status");
        request
            .headers_mut()
            .insert("authorization", authorization.parse().unwrap());
        assert_eq!(
            router.clone().oneshot(request).await.unwrap().status(),
            StatusCode::UNAUTHORIZED
        );
    }

    for path in ["/", "/sessions/deep-link", "/api/v1/status"] {
        let response = router
            .clone()
            .oneshot(authorized_request(
                path,
                "agents-viewer",
                "correct horse:电池订书钉",
            ))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK, "{path}");
    }

    let mut lower_case_scheme = support::request("/api/v1/status");
    lower_case_scheme.headers_mut().insert(
        "authorization",
        basic_authorization("agents-viewer", "correct horse:电池订书钉")
            .replacen("Basic", "basic", 1)
            .parse()
            .unwrap(),
    );
    assert_eq!(
        router
            .clone()
            .oneshot(lower_case_scheme)
            .await
            .unwrap()
            .status(),
        StatusCode::OK
    );

    let mut invalid_host = authorized_request(
        "/api/v1/status",
        "agents-viewer",
        "correct horse:电池订书钉",
    );
    invalid_host
        .headers_mut()
        .insert("host", "evil.example:4747".parse().unwrap());
    let response = router.clone().oneshot(invalid_host).await.unwrap();
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert!(response.headers().get("www-authenticate").is_none());

    let mut post = authorized_request(
        "/api/v1/status",
        "agents-viewer",
        "correct horse:电池订书钉",
    );
    *post.method_mut() = Method::POST;
    assert_eq!(
        router.oneshot(post).await.unwrap().status(),
        StatusCode::METHOD_NOT_ALLOWED
    );
}

fn authorized_request(
    path: &str,
    username: &str,
    password: &str,
) -> http::Request<axum::body::Body> {
    let mut request = support::request(path);
    request.headers_mut().insert(
        "authorization",
        basic_authorization(username, password).parse().unwrap(),
    );
    request
}

fn basic_authorization(username: &str, password: &str) -> String {
    let credentials =
        base64::engine::general_purpose::STANDARD.encode(format!("{username}:{password}"));
    format!("Basic {credentials}")
}

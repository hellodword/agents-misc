mod support;

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

//! Domaine de cotation indépendant du réseau et adaptateur HTTP de santé.

pub mod domain;
mod httpapi;

use axum::{Json, Router, routing::get};
use serde::Serialize;

#[derive(Serialize)]
struct Health {
    status: &'static str,
}

pub fn router() -> Router {
    httpapi::router(domain::Market::demo().expect("configuration de démonstration valide"))
        .route("/healthz", get(health))
}

async fn health() -> Json<Health> {
    Json(Health { status: "ok" })
}

#[cfg(test)]
mod tests {
    use super::router;
    use axum::{
        body::{Body, to_bytes},
        http::{Request, StatusCode, header::CONTENT_TYPE},
    };
    use tower::ServiceExt;

    #[tokio::test]
    async fn health_reports_process_liveness() {
        let response = router()
            .oneshot(Request::get("/healthz").body(Body::empty()).unwrap())
            .await
            .unwrap();

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(response.headers()[CONTENT_TYPE], "application/json");
        let body = to_bytes(response.into_body(), 1024).await.unwrap();
        assert_eq!(body.as_ref(), br#"{"status":"ok"}"#);
    }

    #[tokio::test]
    async fn unsupported_routes_are_rejected() {
        for (method, path, expected) in [
            ("POST", "/healthz", StatusCode::METHOD_NOT_ALLOWED),
            ("GET", "/rfqs", StatusCode::NOT_FOUND),
        ] {
            let response = router()
                .oneshot(
                    Request::builder()
                        .method(method)
                        .uri(path)
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(response.status(), expected);
        }
    }
}

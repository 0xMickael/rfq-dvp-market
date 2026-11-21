//! Domaine de marché indépendant du réseau et adaptateurs HTTP locaux.

pub mod domain;
mod httpapi;
mod metrics;
mod rfqapi;

use axum::{Json, Router, routing::get};
use serde::Serialize;

#[derive(Serialize)]
struct Health {
    status: &'static str,
}

pub fn demo_engine() -> domain::Engine {
    domain::Engine::new(
        domain::Market::demo().expect("configuration de démonstration valide"),
        domain::Timing {
            quote_seconds: 120,
            settlement_seconds: 600,
        },
        || {
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .expect("horloge Unix disponible")
                .as_secs()
        },
    )
    .expect("valid demo deadlines")
}

pub fn router() -> Router {
    router_with_engine(std::sync::Arc::new(demo_engine()))
}

pub(crate) async fn blocking<T: Send + 'static>(
    f: impl FnOnce() -> Result<T, domain::EngineError> + Send + 'static,
) -> Result<T, domain::EngineError> {
    tokio::task::spawn_blocking(f)
        .await
        .map_err(|_| domain::EngineError::Unavailable)?
}

pub fn router_with_engine(engine: std::sync::Arc<domain::Engine>) -> Router {
    let metrics = metrics::Metrics::default();
    httpapi::router(engine.clone())
        .merge(rfqapi::router(engine))
        .route("/healthz", get(health))
        .merge(
            Router::new()
                .route("/metrics", get(metrics::expose))
                .with_state(metrics.clone()),
        )
        .layer(axum::middleware::from_fn_with_state(
            metrics,
            metrics::record,
        ))
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
    async fn market_exposes_exact_read_only_snapshot() {
        let response = router()
            .oneshot(Request::get("/market").body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 16384).await.unwrap();
        let text = String::from_utf8(body.to_vec()).unwrap();
        assert!(text.contains(r#""available_tbill":"1000.000000""#));
        assert!(text.contains(r#""reserved_tbill":"0.000000""#));
        for id in ["MM1", "MM2", "MM3"] {
            assert!(text.contains(id));
        }
    }

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
    async fn metrics_use_route_templates_instead_of_rfq_identifiers() {
        let app = router();
        for id in ["first", "second"] {
            app.clone()
                .oneshot(
                    Request::get(format!("/rfqs/{id}"))
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
        }
        let response = app
            .oneshot(Request::get("/metrics").body(Body::empty()).unwrap())
            .await
            .unwrap();
        let body = to_bytes(response.into_body(), 16384).await.unwrap();
        let text = String::from_utf8(body.to_vec()).unwrap();
        assert!(text.contains("route=\"/rfqs/{rfq_id}\",status=\"4xx\"} 2"));
        assert!(!text.contains("/rfqs/first") && !text.contains("/rfqs/second"));
        assert!(text.contains("le=\"+Inf\""));
    }

    #[tokio::test]
    async fn unsupported_routes_are_rejected() {
        for (method, path, expected) in [
            ("POST", "/healthz", StatusCode::METHOD_NOT_ALLOWED),
            ("GET", "/rfqs", StatusCode::METHOD_NOT_ALLOWED),
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

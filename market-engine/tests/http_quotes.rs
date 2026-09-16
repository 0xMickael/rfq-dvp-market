use axum::{
    body::{Body, to_bytes},
    http::Request,
};
use tower::ServiceExt;

async fn call(body: String) -> (u16, String) {
    let response = rfq_dvp_market_engine::router()
        .oneshot(
            Request::post("/quotes")
                .header("content-type", "application/json")
                .body(Body::from(body))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status().as_u16();
    let bytes = to_bytes(response.into_body(), 65536).await.unwrap();
    (status, String::from_utf8(bytes.to_vec()).unwrap())
}

#[tokio::test]
async fn four_modes_and_inventory_refusals() {
    for side in ["BUY", "SELL"] {
        for unit in ["tBILL", "sEUR"] {
            let (status, body) = call(format!(
                r#"{{"side":"{side}","unit":"{unit}","amount":"100"}}"#
            ))
            .await;
            assert_eq!(status, 200, "{body}");
            let best = if side == "BUY" { "MM2" } else { "MM3" };
            assert!(
                body.starts_with(&format!(r#"{{"quotes":[{{"maker_id":"{best}""#)),
                "{body}"
            );
            assert!(body.contains(r#""unit_price":"98."#));
            assert_eq!(body.matches("maker_id").count(), 3);
        }
        let (status, body) = call(format!(
            r#"{{"side":"{side}","unit":"tBILL","amount":"100000"}}"#
        ))
        .await;
        assert_eq!(status, 422);
        assert!(body.contains("NO_EXECUTABLE_QUOTE"));
        assert_eq!(body.matches("INSUFFICIENT_INVENTORY").count(), 3);
    }
    let (status, body) = call(r#"{"side":"BUY","unit":"tBILL","amount":"1100"}"#.into()).await;
    assert_eq!(status, 200);
    assert_eq!(body.matches("INSUFFICIENT_INVENTORY").count(), 2);
}

#[tokio::test]
async fn invalid_amounts_and_payloads() {
    for amount in ["0", "-1", "0.0000001", "1e3"] {
        assert_eq!(
            call(format!(
                r#"{{"side":"BUY","unit":"tBILL","amount":"{amount}"}}"#
            ))
            .await
            .0,
            400
        );
    }
    for body in [
        r#"{"side":"BUY","unit":"tBILL","amount":100}"#,
        r#"{"side":"BUY","unit":"tBILL","amount":"1","extra":true}"#,
        "null",
        "{}",
    ] {
        let (status, body) = call(body.into()).await;
        assert!((400..500).contains(&status));
        assert!(body.contains("INVALID_REQUEST"));
    }
    assert_eq!(call("x".repeat(5000)).await.0, 413);
}

use axum::{
    Router,
    body::{Body, to_bytes},
    http::Request,
};
use rfq_dvp_market_engine::{domain::*, router_with_engine};
use std::sync::{
    Arc,
    atomic::{AtomicU64, Ordering},
};
use tower::ServiceExt;

fn setup() -> (Router, Arc<Engine>, Arc<AtomicU64>) {
    let time = Arc::new(AtomicU64::new(1000));
    let clock = time.clone();
    let engine = Arc::new(
        Engine::new(Market::demo().unwrap(), Timing::default(), move || {
            clock.load(Ordering::SeqCst)
        })
        .unwrap(),
    );
    (router_with_engine(engine.clone()), engine, time)
}
async fn call(app: &Router, method: &str, path: &str, body: &str) -> (u16, String) {
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header("content-type", "application/json")
                .body(Body::from(body.to_owned()))
                .unwrap(),
        )
        .await
        .unwrap();
    (
        response.status().as_u16(),
        String::from_utf8(
            to_bytes(response.into_body(), 65536)
                .await
                .unwrap()
                .to_vec(),
        )
        .unwrap(),
    )
}

#[tokio::test]
async fn create_accept_and_read_in_all_four_modes() {
    for side in ["BUY", "SELL"] {
        for unit in ["tBILL", "sEUR"] {
            let (app, engine, _) = setup();
            let (status, body) = call(&app, "POST", "/rfqs", &format!(r#"{{"trader_id":"alice","side":"{side}","unit":"{unit}","amount":"100.123456"}}"#)).await;
            assert_eq!(status, 201, "{body}");
            let rfq = engine.rfq("rfq-1").unwrap();
            assert!(body.contains(r#""status":"QUOTED""#));
            assert!(body.contains(r#""quote_expires_at":1030,"settlement_deadline":1300"#));
            assert!(body.contains(&format!(
                r#""quote_id":"{}","trade_id":"{}""#,
                rfq.quotes[0].quote_id, rfq.quotes[0].trade_id
            )));
            let path = format!("/rfqs/{}/accept", rfq.rfq_id);
            let payload = format!(
                r#"{{"trader_id":"alice","quote_id":"{}","trade_id":"{}"}}"#,
                rfq.quotes[0].quote_id, rfq.quotes[0].trade_id
            );
            let (status, body) = call(&app, "POST", &path, &payload).await;
            assert_eq!(status, 200, "{body}");
            assert!(body.contains(r#""status":"RESERVED","settlement_authorized":false"#));
            assert!(body.contains(&format!(
                r#""reserved_asset":"{}""#,
                if side == "BUY" { "tBILL" } else { "sEUR" }
            )));
            assert!(body.contains(&format!(
                r#""cash_amount":"{}""#,
                rfq.quotes[0].terms.cash_amount
            )));
            assert!(body.contains(&format!(
                r#""quantity_tbill":"{}""#,
                rfq.quotes[0].terms.quantity_tbill
            )));
            assert_eq!(call(&app, "POST", &path, &payload).await.0, 409);
            let (status, stored) = call(&app, "GET", &format!("/rfqs/{}", rfq.rfq_id), "").await;
            assert_eq!(status, 200);
            assert!(stored.contains(r#""status":"ACCEPTED""#));
            assert!(stored.contains(&body));
        }
    }
}

#[tokio::test]
async fn expiry_unknown_ids_identity_and_untrusted_terms_are_rejected() {
    let (app, engine, time) = setup();
    let rfq = engine
        .create_rfq(
            "alice",
            QuoteRequest {
                side: Side::Buy,
                amount: RequestAmount::Tbill("100".parse().unwrap()),
            },
        )
        .unwrap();
    let path = format!("/rfqs/{}/accept", rfq.rfq_id);
    for (payload, expected, code) in [
        (
            format!(
                r#"{{"trader_id":"bob","quote_id":"{}","trade_id":"{}"}}"#,
                rfq.quotes[0].quote_id, rfq.quotes[0].trade_id
            ),
            403,
            "WRONG_TRADER",
        ),
        (
            r#"{"trader_id":"alice","quote_id":"missing","trade_id":"missing"}"#.into(),
            404,
            "UNKNOWN_QUOTE",
        ),
        (
            format!(
                r#"{{"trader_id":"alice","quote_id":"{}","trade_id":"{}","cash_amount":"1"}}"#,
                rfq.quotes[0].quote_id, rfq.quotes[0].trade_id
            ),
            422,
            "INVALID_REQUEST",
        ),
    ] {
        let (status, body) = call(&app, "POST", &path, &payload).await;
        assert_eq!(status, expected, "{body}");
        assert!(body.contains(code));
    }
    assert_eq!(call(&app, "GET", "/rfqs/missing", "").await.0, 404);
    time.store(1030, Ordering::SeqCst);
    let payload = format!(
        r#"{{"trader_id":"alice","quote_id":"{}","trade_id":"{}"}}"#,
        rfq.quotes[0].quote_id, rfq.quotes[0].trade_id
    );
    let (status, body) = call(&app, "POST", &path, &payload).await;
    assert_eq!(status, 410);
    assert!(body.contains("QUOTE_EXPIRED"));
    assert!(
        call(&app, "GET", &format!("/rfqs/{}", rfq.rfq_id), "")
            .await
            .1
            .contains(r#""status":"EXPIRED""#)
    );
    assert_eq!(
        engine.maker_inventory("MM2").unwrap().unwrap().1,
        Inventory::default()
    );
}

#[tokio::test]
async fn invalid_rfq_requests_and_inventory_conflicts_are_explicit() {
    let (app, engine, _) = setup();
    for (payload, expected) in [
        (
            r#"{"trader_id":"","side":"BUY","unit":"tBILL","amount":"1"}"#,
            400,
        ),
        (
            r#"{"trader_id":"alice","side":"BUY","unit":"tBILL","amount":"0.0000001"}"#,
            400,
        ),
        (
            r#"{"trader_id":"alice","side":"BUY","unit":"tBILL","amount":"1000000"}"#,
            422,
        ),
        (
            r#"{"trader_id":"alice","side":"BUY","unit":"tBILL","amount":100}"#,
            422,
        ),
    ] {
        assert_eq!(call(&app, "POST", "/rfqs", payload).await.0, expected);
    }
    assert_eq!(call(&app, "POST", "/rfqs", &"x".repeat(5000)).await.0, 413);
    let request = QuoteRequest {
        side: Side::Buy,
        amount: RequestAmount::Tbill("1000".parse().unwrap()),
    };
    let a = engine.create_rfq("alice", request).unwrap();
    let b = engine.create_rfq("bob", request).unwrap();
    engine
        .accept(&a.rfq_id, &a.quotes[0].quote_id, "alice")
        .unwrap();
    let (status, body) = call(
        &app,
        "POST",
        &format!("/rfqs/{}/accept", b.rfq_id),
        &format!(
            r#"{{"trader_id":"bob","quote_id":"{}","trade_id":"{}"}}"#,
            b.quotes[0].quote_id, b.quotes[0].trade_id
        ),
    )
    .await;
    assert_eq!(status, 409);
    assert!(body.contains("INSUFFICIENT_INVENTORY"));
    assert_eq!(engine.rfq(&b.rfq_id).unwrap().status, RfqStatus::Quoted);
    let (status, body) = call(
        &app,
        "POST",
        "/quotes",
        r#"{"side":"BUY","unit":"tBILL","amount":"1000"}"#,
    )
    .await;
    assert_eq!(status, 200);
    assert!(body.contains("INSUFFICIENT_INVENTORY"));
    assert!(!body.contains(r#""quotes":[{"maker_id":"MM2""#));
}

#[tokio::test]
async fn settlement_notifications_are_bound_to_the_trade_and_idempotent() {
    let (app, engine, _) = setup();
    let rfq = engine
        .create_rfq(
            "alice",
            QuoteRequest {
                side: Side::Buy,
                amount: RequestAmount::Tbill("1".parse().unwrap()),
            },
        )
        .unwrap();
    let trade = engine
        .accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, "alice")
        .unwrap();
    let path = format!("/rfqs/{}/settlement", rfq.rfq_id);
    let payload = |id: &str, status: &str, timestamp: u64| {
        format!(
            r#"{{"trade_id":"{id}","status":"{status}","transaction_hash":"0x{}","block_timestamp":{timestamp}}}"#,
            "1".repeat(64)
        )
    };
    assert_eq!(
        call(&app, "POST", &path, &payload("wrong", "SETTLED", 1002))
            .await
            .0,
        409
    );
    assert_eq!(
        call(
            &app,
            "POST",
            &path,
            &payload(&trade.trade_id, "SETTLEMENT_FAILED", 1299)
        )
        .await
        .0,
        409
    );
    for _ in 0..2 {
        let (status, body) = call(
            &app,
            "POST",
            &path,
            &payload(&trade.trade_id, "SETTLED", 1002),
        )
        .await;
        assert_eq!(status, 200, "{body}");
        assert!(body.contains("SETTLED"));
    }
    assert_eq!(
        call(
            &app,
            "POST",
            &path,
            &payload(&trade.trade_id, "SETTLEMENT_PENDING", 0)
        )
        .await
        .0,
        409
    );
}

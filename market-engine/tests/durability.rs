use rfq_dvp_market_engine::domain::{
    Engine, Market, QuoteRequest, RequestAmount, RfqStatus, Side, Timing, TradeStatus,
};
use std::sync::{
    Arc,
    atomic::{AtomicU64, Ordering},
};

#[test]
#[ignore = "requires a new dedicated PostgreSQL database; scripts/test-recovery.ps1 runs this"]
fn durable_clock_ids_reservations_and_accounting() {
    let url = std::env::var("RFQ_TEST_DATABASE_URL").expect("dedicated test database required");
    let mut db = postgres::Client::connect(&url, postgres::NoTls).unwrap();
    db.batch_execute(include_str!("../../migrations/001_durability.sql"))
        .unwrap();
    assert_eq!(
        db.query_one("SELECT count(*) FROM market_state", &[])
            .unwrap()
            .get::<_, i64>(0),
        0,
        "never overwrite an existing market"
    );
    let clock = Arc::new(AtomicU64::new(100));
    let open = || {
        let clock = clock.clone();
        Engine::new(Market::demo().unwrap(), Timing::default(), move || {
            clock.load(Ordering::SeqCst)
        })
        .unwrap()
        .persist(&url, "durability-test")
    };
    let request = QuoteRequest {
        side: Side::Buy,
        amount: RequestAmount::Tbill("100".parse().unwrap()),
    };
    let engine = open().unwrap();
    assert!(open().is_err(), "second writer must be refused");
    let quoted = engine.create_rfq("alice", request).unwrap();
    let reserved = engine.create_rfq("bob", request).unwrap();
    let trade = engine
        .accept(&reserved.rfq_id, &reserved.quotes[0].quote_id, "bob")
        .unwrap();
    let inventory = engine.market_snapshot().unwrap();
    drop(engine);
    clock.store(131, Ordering::SeqCst);
    let engine = open().unwrap();
    assert_eq!(engine.market_snapshot().unwrap(), inventory);
    assert_eq!(
        engine.rfq(&reserved.rfq_id).unwrap().trade,
        Some(trade.clone())
    );
    assert_eq!(
        engine.rfq(&quoted.rfq_id).unwrap().status,
        RfqStatus::Expired
    );
    drop(engine);
    clock.store(90, Ordering::SeqCst);
    let engine = open().unwrap();
    assert_eq!(
        engine.rfq(&quoted.rfq_id).unwrap().status,
        RfqStatus::Expired,
        "clock rollback must not reopen a quote after restart"
    );
    let newer = engine.create_rfq("carol", request).unwrap();
    assert_ne!(newer.rfq_id, quoted.rfq_id);
    assert_ne!(newer.quotes[0].trade_id, trade.trade_id);
    let hash = Some(format!("0x{}", "ab".repeat(32)));
    engine
        .settlement(
            &reserved.rfq_id,
            &trade.trade_id,
            TradeStatus::Settled,
            hash.clone(),
            150,
        )
        .unwrap();
    let settled = engine.market_snapshot().unwrap();
    drop(engine);
    let engine = open().unwrap();
    engine
        .settlement(
            &reserved.rfq_id,
            &trade.trade_id,
            TradeStatus::Settled,
            hash,
            150,
        )
        .unwrap();
    assert_eq!(
        engine.market_snapshot().unwrap(),
        settled,
        "no second accounting after restart"
    );
}

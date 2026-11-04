use rfq_dvp_market_engine::domain::*;
use std::sync::{
    Arc, Barrier,
    atomic::{AtomicU64, Ordering},
};

fn a(s: &str) -> Amount {
    s.parse().unwrap()
}
fn request(side: Side) -> QuoteRequest {
    QuoteRequest {
        side,
        amount: RequestAmount::Tbill(a("100")),
    }
}
fn setup(market: Market) -> (Arc<Engine>, Arc<AtomicU64>) {
    let time = Arc::new(AtomicU64::new(1000));
    let clock = time.clone();
    (
        Arc::new(
            Engine::new(market, Timing::default(), move || {
                clock.load(Ordering::SeqCst)
            })
            .unwrap(),
        ),
        time,
    )
}
fn limited_market() -> Market {
    Market::new(
        ReferenceMarket {
            face_value: a("1"),
            annual_rate: Amount::ZERO,
            maturity_years: Amount::ZERO,
        },
        vec![MarketMaker {
            id: "MM".into(),
            inventory: Inventory {
                tbill: a("150"),
                cash: a("150"),
            },
            reserved: Inventory::default(),
            target_tbill: a("150"),
            inventory_skew: Amount::ZERO,
            spread: Amount::ZERO,
        }],
    )
    .unwrap()
}
fn race(engine: &Arc<Engine>, rfqs: [Rfq; 2]) -> Vec<Result<Trade, EngineError>> {
    let barrier = Arc::new(Barrier::new(3));
    let handles: Vec<_> = rfqs
        .into_iter()
        .map(|rfq| {
            let engine = engine.clone();
            let barrier = barrier.clone();
            std::thread::spawn(move || {
                barrier.wait();
                engine.accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, &rfq.trader_id)
            })
        })
        .collect();
    barrier.wait();
    handles.into_iter().map(|h| h.join().unwrap()).collect()
}

#[test]
fn concurrent_requests_cannot_reserve_200_against_150_in_either_asset() {
    for side in [Side::Buy, Side::Sell] {
        let (engine, _) = setup(limited_market());
        let rfqs = [
            engine.create_rfq("alice", request(side)).unwrap(),
            engine.create_rfq("bob", request(side)).unwrap(),
        ];
        let results = race(&engine, rfqs.clone());
        assert_eq!(results.iter().filter(|r| r.is_ok()).count(), 1);
        let asset = if side == Side::Buy {
            Asset::Tbill
        } else {
            Asset::Cash
        };
        assert!(results.contains(&Err(EngineError::InsufficientInventory {
            asset,
            required: a("100"),
            available: a("50")
        })));
        let (inventory, reserved) = engine.maker_inventory("MM").unwrap().unwrap();
        assert_eq!(
            inventory,
            Inventory {
                tbill: a("150"),
                cash: a("150")
            }
        );
        assert_eq!(
            reserved,
            if side == Side::Buy {
                Inventory {
                    tbill: a("100"),
                    cash: Amount::ZERO,
                }
            } else {
                Inventory {
                    tbill: Amount::ZERO,
                    cash: a("100"),
                }
            }
        );
        for (rfq, result) in rfqs.iter().zip(results) {
            let stored = engine.rfq(&rfq.rfq_id).unwrap();
            assert_eq!(
                stored.status,
                if result.is_ok() {
                    RfqStatus::Accepted
                } else {
                    RfqStatus::Quoted
                }
            );
            assert_eq!(stored.trade.is_some(), result.is_ok());
        }
        assert!(engine.indicative_quote(request(side)).is_err());
        assert!(
            engine
                .indicative_quote(request(if side == Side::Buy {
                    Side::Sell
                } else {
                    Side::Buy
                }))
                .is_ok()
        );
    }
}

#[test]
fn concurrent_double_acceptance_and_sibling_quote_are_rejected() {
    for side in [Side::Buy, Side::Sell] {
        let (engine, _) = setup(Market::demo().unwrap());
        let rfq = engine.create_rfq("alice", request(side)).unwrap();
        let results = race(&engine, [rfq.clone(), rfq.clone()]);
        assert_eq!(results.iter().filter(|r| r.is_ok()).count(), 1);
        assert!(results.contains(&Err(EngineError::AlreadyAccepted)));
        assert_eq!(
            engine.accept(&rfq.rfq_id, &rfq.quotes[1].quote_id, "alice"),
            Err(EngineError::AlreadyAccepted)
        );
        let trade = results.into_iter().find_map(Result::ok).unwrap();
        assert_eq!(engine.rfq(&rfq.rfq_id).unwrap().trade, Some(trade.clone()));
        let (_, reserved) = engine
            .maker_inventory(&trade.quote.terms.maker_id)
            .unwrap()
            .unwrap();
        assert_eq!(
            if side == Side::Buy {
                reserved.tbill
            } else {
                reserved.cash
            },
            trade.reserved_amount
        );
    }
}

#[test]
fn exact_expiry_is_exclusive_and_time_never_releases_reservations() {
    for side in [Side::Buy, Side::Sell] {
        for offset in [29, 30, 31] {
            let (engine, time) = setup(Market::demo().unwrap());
            let rfq = engine.create_rfq("alice", request(side)).unwrap();
            assert_eq!(
                (rfq.quote_expires_at, rfq.settlement_deadline),
                (1030, 1300)
            );
            time.store(1000 + offset, Ordering::SeqCst);
            let result = engine.accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, "alice");
            if offset == 29 {
                let trade = result.unwrap();
                let before = engine.maker_inventory(&trade.quote.terms.maker_id).unwrap();
                time.store(1400, Ordering::SeqCst);
                assert_eq!(engine.rfq(&rfq.rfq_id).unwrap().status, RfqStatus::Accepted);
                assert_eq!(
                    engine.maker_inventory(&trade.quote.terms.maker_id).unwrap(),
                    before
                );
                assert_eq!(trade.settlement_deadline, 1300);
            } else {
                assert_eq!(result, Err(EngineError::Expired));
                time.store(1000, Ordering::SeqCst);
                assert_eq!(
                    engine.accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, "alice"),
                    Err(EngineError::Expired)
                );
                assert_eq!(engine.rfq(&rfq.rfq_id).unwrap().status, RfqStatus::Expired);
                assert!(engine.rfq(&rfq.rfq_id).unwrap().trade.is_none());
                assert_eq!(
                    engine
                        .maker_inventory(&rfq.quotes[0].terms.maker_id)
                        .unwrap()
                        .unwrap()
                        .1,
                    Inventory::default()
                );
            }
        }
    }
}

#[test]
fn preserves_exact_terms_for_all_four_modes_and_reserves_only_outgoing_asset() {
    for side in [Side::Buy, Side::Sell] {
        for cash in [false, true] {
            let (engine, _) = setup(Market::demo().unwrap());
            let req = QuoteRequest {
                side,
                amount: if cash {
                    RequestAmount::Cash(a("1000.123456"))
                } else {
                    RequestAmount::Tbill(a("12.345678"))
                },
            };
            let rfq = engine.create_rfq("alice", req).unwrap();
            let quote = rfq.quotes[0].clone();
            let before = engine
                .maker_inventory(&quote.terms.maker_id)
                .unwrap()
                .unwrap();
            assert_eq!(before.1, Inventory::default());
            let trade = engine
                .accept(&rfq.rfq_id, &quote.quote_id, "alice")
                .unwrap();
            assert_eq!(trade.quote, quote);
            assert_eq!(trade.trader_id, "alice");
            assert_eq!(trade.trade_id.len(), 66);
            let after = engine
                .maker_inventory(&quote.terms.maker_id)
                .unwrap()
                .unwrap();
            assert_eq!(after.0, before.0);
            assert_eq!(
                after.1,
                if side == Side::Buy {
                    Inventory {
                        tbill: quote.terms.quantity_tbill,
                        cash: Amount::ZERO,
                    }
                } else {
                    Inventory {
                        tbill: Amount::ZERO,
                        cash: quote.terms.cash_amount,
                    }
                }
            );
            for other in rfq.quotes.iter().skip(1) {
                assert_eq!(
                    engine
                        .maker_inventory(&other.terms.maker_id)
                        .unwrap()
                        .unwrap()
                        .1,
                    Inventory::default()
                );
            }
        }
    }
}

#[test]
fn invalid_identity_ids_and_cross_rfq_quote_do_not_mutate_reservations() {
    let (engine, _) = setup(Market::demo().unwrap());
    let rfq = engine.create_rfq("alice", request(Side::Buy)).unwrap();
    let other = engine.create_rfq("bob", request(Side::Buy)).unwrap();
    assert_ne!(rfq.rfq_id, other.rfq_id);
    for q in &rfq.quotes {
        assert!(!other.quotes.iter().any(|o| o.quote_id == q.quote_id));
    }
    assert_eq!(
        engine.accept("missing", &rfq.quotes[0].quote_id, "alice"),
        Err(EngineError::UnknownRfq)
    );
    assert_eq!(
        engine.accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, "bob"),
        Err(EngineError::WrongTrader)
    );
    assert_eq!(
        engine.accept(&rfq.rfq_id, "missing", "alice"),
        Err(EngineError::UnknownQuote)
    );
    assert_eq!(
        engine.accept(&rfq.rfq_id, &other.quotes[0].quote_id, "alice"),
        Err(EngineError::UnknownQuote)
    );
    for id in ["", " alice", "alice bob", &"a".repeat(65)] {
        assert_eq!(
            engine.create_rfq(id, request(Side::Buy)),
            Err(EngineError::InvalidTrader)
        );
    }
    assert_eq!(engine.rfq(&rfq.rfq_id).unwrap(), rfq);
    assert_eq!(
        engine.maker_inventory("MM2").unwrap().unwrap().1,
        Inventory::default()
    );
    let first = engine
        .accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, "alice")
        .unwrap();
    let second = engine
        .accept(&other.rfq_id, &other.quotes[0].quote_id, "bob")
        .unwrap();
    assert_ne!(first.trade_id, second.trade_id);
}

#[test]
fn invalid_timing_overflow_and_no_quote_fail_without_reservation() {
    for timing in [
        Timing {
            quote_seconds: 0,
            settlement_seconds: 10,
        },
        Timing {
            quote_seconds: 10,
            settlement_seconds: 10,
        },
    ] {
        assert!(Engine::new(limited_market(), timing, || 0).is_err());
    }
    let (engine, time) = setup(limited_market());
    time.store(u64::MAX, Ordering::SeqCst);
    assert_eq!(
        engine.create_rfq("alice", request(Side::Buy)),
        Err(EngineError::Market(MarketError::ArithmeticOverflow))
    );
    assert_eq!(
        engine.maker_inventory("MM").unwrap().unwrap().1,
        Inventory::default()
    );
    let (engine, _) = setup(limited_market());
    assert!(matches!(
        engine.create_rfq(
            "alice",
            QuoteRequest {
                side: Side::Buy,
                amount: RequestAmount::Tbill(a("151"))
            }
        ),
        Err(EngineError::Market(MarketError::NoExecutableQuote(_)))
    ));
    assert_eq!(
        engine.maker_inventory("MM").unwrap().unwrap().1,
        Inventory::default()
    );
}

#[test]
fn trade_id_is_fixed_before_acceptance_and_bound_to_the_engine_session() {
    let (engine, _) = setup(limited_market());
    let rfq = engine.create_rfq("alice", request(Side::Buy)).unwrap();
    let (other, _) = setup(limited_market());
    let other_rfq = other.create_rfq("alice", request(Side::Buy)).unwrap();
    let q = &rfq.quotes[0];
    assert_ne!(q.trade_id, other_rfq.quotes[0].trade_id);
    assert_eq!(
        engine.accept_expected(
            &rfq.rfq_id,
            &q.quote_id,
            "alice",
            Some(&other_rfq.quotes[0].trade_id)
        ),
        Err(EngineError::WrongTradeId)
    );
    assert_eq!(
        engine.maker_inventory("MM").unwrap().unwrap().1,
        Inventory::default()
    );
    let trade = engine
        .accept_expected(&rfq.rfq_id, &q.quote_id, "alice", Some(&q.trade_id))
        .unwrap();
    assert_eq!(trade.trade_id, q.trade_id);
}

#[test]
fn settlement_is_atomic_and_idempotent_for_both_sides() {
    for side in [Side::Buy, Side::Sell] {
        let (engine, _) = setup(limited_market());
        let rfq = engine.create_rfq("alice", request(side)).unwrap();
        let trade = engine
            .accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, "alice")
            .unwrap();
        let hash = Some(format!("0x{}", "1".repeat(64)));
        assert_eq!(
            engine.settlement(
                &rfq.rfq_id,
                &trade.trade_id,
                TradeStatus::SettlementFailed,
                None,
                1299
            ),
            Err(EngineError::InvalidTransition)
        );
        engine
            .settlement(
                &rfq.rfq_id,
                &trade.trade_id,
                TradeStatus::SettlementPending,
                hash.clone(),
                0,
            )
            .unwrap();
        let barrier = Arc::new(Barrier::new(3));
        let handles: Vec<_> = (0..2)
            .map(|_| {
                let engine = engine.clone();
                let barrier = barrier.clone();
                let rfq_id = rfq.rfq_id.clone();
                let id = trade.trade_id.clone();
                let hash = hash.clone();
                std::thread::spawn(move || {
                    barrier.wait();
                    engine
                        .settlement(&rfq_id, &id, TradeStatus::Settled, hash, 1002)
                        .unwrap()
                })
            })
            .collect();
        barrier.wait();
        for h in handles {
            assert_eq!(h.join().unwrap().status, TradeStatus::Settled);
        }
        let (inventory, reserved) = engine.maker_inventory("MM").unwrap().unwrap();
        assert_eq!(reserved, Inventory::default());
        assert_eq!(
            inventory.tbill,
            a(if side == Side::Buy { "50" } else { "250" })
        );
        assert_eq!(
            inventory.cash,
            a(if side == Side::Buy { "250" } else { "50" })
        );
        assert_eq!(
            engine.settlement(
                &rfq.rfq_id,
                &trade.trade_id,
                TradeStatus::SettlementFailed,
                None,
                1300
            ),
            Err(EngineError::InvalidTransition)
        );
    }
}

#[test]
fn expiry_release_needs_chain_deadline_and_never_credits_incoming_assets() {
    for side in [Side::Buy, Side::Sell] {
        let (engine, _) = setup(limited_market());
        let rfq = engine.create_rfq("alice", request(side)).unwrap();
        let trade = engine
            .accept(&rfq.rfq_id, &rfq.quotes[0].quote_id, "alice")
            .unwrap();
        assert_eq!(
            engine.settlement(
                &rfq.rfq_id,
                "wrong",
                TradeStatus::SettlementFailed,
                None,
                1300
            ),
            Err(EngineError::WrongTradeId)
        );
        for _ in 0..2 {
            engine
                .settlement(
                    &rfq.rfq_id,
                    &trade.trade_id,
                    TradeStatus::SettlementFailed,
                    None,
                    1300,
                )
                .unwrap();
        }
        let (inventory, reserved) = engine.maker_inventory("MM").unwrap().unwrap();
        assert_eq!(
            inventory,
            Inventory {
                cash: a("150"),
                tbill: a("150")
            }
        );
        assert_eq!(reserved, Inventory::default());
        assert_eq!(
            engine.settlement(
                &rfq.rfq_id,
                &trade.trade_id,
                TradeStatus::Settled,
                Some(format!("0x{}", "1".repeat(64))),
                1002
            ),
            Err(EngineError::InvalidTransition)
        );
    }
}

use rfq_dvp_market_engine::domain::{
    Amount, Asset, Inventory, Market, MarketError, MarketMaker, QuoteRequest, ReferenceMarket,
    RejectionReason, RequestAmount, SCALE, Side,
};

fn amount(value: &str) -> Amount {
    value.parse().unwrap()
}

fn reference(price: &str) -> ReferenceMarket {
    ReferenceMarket {
        face_value: amount(price),
        annual_rate: Amount::ZERO,
        maturity_years: Amount::ZERO,
    }
}

fn maker(id: &str, tbill: &str, cash: &str) -> MarketMaker {
    MarketMaker {
        id: id.to_owned(),
        inventory: Inventory {
            tbill: amount(tbill),
            cash: amount(cash),
        },
        reserved: Inventory::default(),
        target_tbill: amount("1"),
        inventory_skew: Amount::ZERO,
        spread: Amount::ZERO,
    }
}

fn request(side: Side, value: &str, in_cash: bool) -> QuoteRequest {
    QuoteRequest {
        side,
        amount: if in_cash {
            RequestAmount::Cash(amount(value))
        } else {
            RequestAmount::Tbill(amount(value))
        },
    }
}

#[test]
fn decimal_input_preserves_six_digits_and_normalizes_display() {
    for (input, atoms, output) in [
        ("0", 0, "0.000000"),
        ("0012.5", 12_500_000, "12.500000"),
        ("0.000001", 1, "0.000001"),
        ("12.345678", 12_345_678, "12.345678"),
        ("18446744073709.551615", u64::MAX, "18446744073709.551615"),
    ] {
        let parsed = amount(input);
        assert_eq!(parsed.atoms(), atoms);
        assert_eq!(parsed.to_string(), output);
        assert_eq!(parsed.to_string().parse::<Amount>().unwrap(), parsed);
    }
}

#[test]
fn malformed_and_overprecise_input_is_rejected_without_rounding() {
    for input in [
        "", "-1", "+1", "1e3", "NaN", "inf", "1,5", " 1", "1 ", ".5", "1.", "1.2.3", "１",
    ] {
        assert_eq!(
            input.parse::<Amount>(),
            Err(MarketError::InvalidDecimal),
            "{input}"
        );
    }
    assert_eq!(
        "0.1234567".parse::<Amount>(),
        Err(MarketError::TooManyDecimals)
    );
    for input in [
        "18446744073709.551616",
        "18446744073710",
        "999999999999999999999999999999",
    ] {
        assert_eq!(
            input.parse::<Amount>(),
            Err(MarketError::ArithmeticOverflow)
        );
    }
}

#[test]
fn reference_price_matches_the_documented_discount_formula() {
    let reference = ReferenceMarket {
        face_value: amount("100"),
        annual_rate: amount("0.04"),
        maturity_years: amount("0.5"),
    };
    assert_eq!(reference.price().unwrap(), amount("98.039215"));
    assert_eq!(
        ReferenceMarket {
            maturity_years: Amount::ZERO,
            ..reference
        }
        .price()
        .unwrap(),
        amount("100")
    );
}

#[test]
fn invalid_reference_parameters_are_rejected() {
    for invalid in [
        reference("0"),
        ReferenceMarket {
            annual_rate: amount("1.000001"),
            ..reference("100")
        },
        ReferenceMarket {
            maturity_years: amount("100.000001"),
            ..reference("100")
        },
        ReferenceMarket {
            annual_rate: amount("1"),
            maturity_years: amount("100"),
            ..reference("0.000001")
        },
    ] {
        assert!(matches!(
            invalid.price(),
            Err(MarketError::InvalidConfiguration(_))
        ));
    }
}

#[test]
fn excess_inventory_lowers_both_prices_and_shortage_raises_them() {
    let mut mm = maker("MM1", "1", "1000");
    mm.inventory_skew = amount("1");
    mm.spread = amount("0.1");
    let at_target = mm.prices(amount("100")).unwrap();
    assert_eq!(at_target.bid, amount("99.95"));
    assert_eq!(at_target.ask, amount("100.05"));
    mm.inventory.tbill = amount("2");
    let excess = mm.prices(amount("100")).unwrap();
    assert_eq!(excess.ask, amount("99.05"));
    mm.inventory.tbill = Amount::ZERO;
    let shortage = mm.prices(amount("100")).unwrap();
    assert_eq!(shortage.bid, amount("100.95"));
    assert!(excess.bid < at_target.bid && at_target.bid < shortage.bid);
}

#[test]
fn odd_spread_rounds_prices_outward() {
    let mut mm = maker("MM1", "1", "10");
    mm.spread = amount("0.000001");
    let prices = mm.prices(amount("1")).unwrap();
    assert_eq!(prices.bid, amount("0.999999"));
    assert_eq!(prices.ask, amount("1.000001"));
}

#[test]
fn demo_quotes_both_sides_and_selects_the_correct_market_maker() {
    let market = Market::demo().unwrap();
    assert_eq!(market.reference_price(), amount("98.039215"));
    for (side, id, price, cash) in [
        (Side::Buy, "MM2", "98.049215", "9804.9215"),
        (Side::Sell, "MM3", "98.009215", "9800.9215"),
    ] {
        let book = market.quote(request(side, "100", false)).unwrap();
        assert_eq!(book.quotes.len(), 3);
        assert!(book.rejections.is_empty());
        let best = book.best().unwrap();
        assert_eq!(best.maker_id, id);
        assert_eq!(best.unit_price, amount(price));
        assert_eq!(best.cash_amount, amount(cash));
        assert_eq!(best.quantity_tbill, amount("100"));
    }
}

#[test]
fn quantities_are_variable_and_fractional_in_both_directions() {
    let market = Market::demo().unwrap();
    for quantity in ["0.000001", "0.25", "12.345678", "137"] {
        for side in [Side::Buy, Side::Sell] {
            let book = market.quote(request(side, quantity, false)).unwrap();
            assert_eq!(book.quotes.len(), 3);
            assert!(
                book.quotes
                    .iter()
                    .all(|quote| quote.quantity_tbill == amount(quantity))
            );
        }
    }
}

#[test]
fn same_snapshot_and_request_produce_identical_quotes_without_reserving() {
    let market = Market::demo().unwrap();
    let before = market.clone();
    let request = request(Side::Buy, "1000", true);
    assert_eq!(
        market.quote(request).unwrap(),
        market.quote(request).unwrap()
    );
    assert_eq!(market, before);
}

#[test]
fn cash_input_is_a_buy_budget_and_a_sell_target() {
    let market = Market::new(reference("3"), vec![maker("MM1", "100", "1000")]).unwrap();
    let buy = market.quote(request(Side::Buy, "10", true)).unwrap();
    assert_eq!(buy.best().unwrap().quantity_tbill, amount("3.333333"));
    assert_eq!(buy.best().unwrap().cash_amount, amount("9.999999"));
    let sell = market.quote(request(Side::Sell, "10", true)).unwrap();
    assert_eq!(sell.best().unwrap().quantity_tbill, amount("3.333334"));
    assert_eq!(sell.best().unwrap().cash_amount, amount("10.000002"));
}

#[test]
fn cash_rounding_does_not_overcharge_a_seller_or_undercharge_a_buyer() {
    let market = Market::new(reference("1.5"), vec![maker("MM1", "1", "10")]).unwrap();
    let buy = market.quote(request(Side::Buy, "0.000001", false)).unwrap();
    let sell = market
        .quote(request(Side::Sell, "0.000001", false))
        .unwrap();
    assert_eq!(buy.best().unwrap().cash_amount, amount("0.000002"));
    assert_eq!(sell.best().unwrap().cash_amount, amount("0.000001"));
}

#[test]
fn cash_mode_rounding_bounds_hold_across_prices_and_amounts() {
    let unlimited = MarketMaker {
        inventory: Inventory {
            tbill: Amount::from_atoms(u64::MAX),
            cash: Amount::from_atoms(u64::MAX),
        },
        ..maker("MM1", "1", "1")
    };
    for price_atoms in [
        1_u64,
        2,
        3,
        999_999,
        1_000_000,
        1_500_001,
        98_060_000,
        100_000_001,
    ] {
        let market = Market::new(
            ReferenceMarket {
                face_value: Amount::from_atoms(price_atoms),
                ..reference("1")
            },
            vec![unlimited.clone()],
        )
        .unwrap();
        for cash_atoms in [
            1_u64,
            2,
            3,
            17,
            99,
            1000,
            1_000_000,
            10_000_001,
            999_999_999,
        ] {
            for side in [Side::Buy, Side::Sell] {
                let result = market.quote(QuoteRequest {
                    side,
                    amount: RequestAmount::Cash(Amount::from_atoms(cash_atoms)),
                });
                let scaled_cash = u128::from(cash_atoms) * u128::from(SCALE);
                let price = u128::from(price_atoms);
                if side == Side::Buy && scaled_cash < price {
                    let MarketError::NoExecutableQuote(rejections) = result.unwrap_err() else {
                        panic!("expected error")
                    };
                    assert_eq!(rejections[0].reason, RejectionReason::BelowMinimumAmount);
                    continue;
                }
                let book = result.unwrap();
                let quote = book.best().unwrap();
                let quantity = u128::from(quote.quantity_tbill.atoms());
                let paid = u128::from(quote.cash_amount.atoms()) * u128::from(SCALE);
                let exact_product = quantity * price;
                match side {
                    Side::Buy => {
                        assert!(quote.cash_amount.atoms() <= cash_atoms);
                        assert!(paid >= exact_product && paid - exact_product < u128::from(SCALE));
                        assert!(
                            (quantity + 1) * price > scaled_cash,
                            "the budget would allow one more atom"
                        );
                    }
                    Side::Sell => {
                        assert!(quote.cash_amount.atoms() >= cash_atoms);
                        assert!(paid <= exact_product && exact_product - paid < u128::from(SCALE));
                        assert!(
                            (quantity - 1) * price < scaled_cash,
                            "the target would be reached with one less atom"
                        );
                    }
                }
            }
        }
    }
}

#[test]
fn inventory_checks_the_asset_leaving_the_market_maker() {
    let buy_market = Market::new(reference("3"), vec![maker("MM1", "100", "0")]).unwrap();
    assert!(buy_market.quote(request(Side::Buy, "100", false)).is_ok());
    let sell_market = Market::new(reference("3"), vec![maker("MM1", "0", "300")]).unwrap();
    assert!(sell_market.quote(request(Side::Sell, "100", false)).is_ok());
}

#[test]
fn existing_reservations_reduce_availability_in_both_assets() {
    let mut mm = maker("MM1", "150", "100");
    mm.reserved = Inventory {
        tbill: amount("50"),
        cash: amount("40"),
    };
    let market = Market::new(reference("3"), vec![mm]).unwrap();
    for (side, exact, excessive, asset, available) in [
        (Side::Buy, "100", "100.000001", Asset::Tbill, "100"),
        (Side::Sell, "20", "20.000001", Asset::Cash, "60"),
    ] {
        assert!(market.quote(request(side, exact, false)).is_ok());
        let MarketError::NoExecutableQuote(rejections) =
            market.quote(request(side, excessive, false)).unwrap_err()
        else {
            panic!("expected rejection")
        };
        let RejectionReason::InsufficientInventory {
            asset: actual_asset,
            available: actual_available,
            required,
        } = rejections[0].reason
        else {
            panic!("expected insufficient inventory")
        };
        assert_eq!(actual_asset, asset);
        assert_eq!(actual_available, amount(available));
        assert!(required > actual_available);
    }
}

#[test]
fn cash_target_uses_actual_rounded_payout_for_inventory_check() {
    let market = Market::new(reference("3"), vec![maker("MM1", "100", "10")]).unwrap();
    let MarketError::NoExecutableQuote(rejections) =
        market.quote(request(Side::Sell, "10", true)).unwrap_err()
    else {
        panic!("expected rejection")
    };
    assert_eq!(
        rejections[0].reason,
        RejectionReason::InsufficientInventory {
            asset: Asset::Cash,
            required: amount("10.000002"),
            available: amount("10"),
        }
    );
}

#[test]
fn a_small_market_maker_is_rejected_without_reducing_the_requested_amount() {
    let market = Market::new(
        reference("3"),
        vec![maker("MM1", "2", "100"), maker("MM2", "100", "100")],
    )
    .unwrap();
    let book = market.quote(request(Side::Buy, "10", true)).unwrap();
    assert_eq!(book.quotes.len(), 1);
    assert_eq!(book.best().unwrap().maker_id, "MM2");
    assert_eq!(book.best().unwrap().quantity_tbill, amount("3.333333"));
    assert_eq!(book.rejections.len(), 1);
    assert_eq!(book.rejections[0].maker_id, "MM1");
}

#[test]
fn liquidity_is_not_aggregated_across_market_makers() {
    let market = Market::new(
        reference("3"),
        vec![maker("MM1", "60", "200"), maker("MM2", "60", "200")],
    )
    .unwrap();
    for side in [Side::Buy, Side::Sell] {
        let MarketError::NoExecutableQuote(rejections) =
            market.quote(request(side, "100", false)).unwrap_err()
        else {
            panic!("expected rejection")
        };
        assert_eq!(rejections.len(), 2);
    }
}

#[test]
fn tied_prices_use_maker_id_not_input_order() {
    let a = maker("MM1", "100", "1000");
    let b = maker("MM2", "100", "1000");
    let forward = Market::new(reference("3"), vec![a.clone(), b.clone()]).unwrap();
    let reverse = Market::new(reference("3"), vec![b, a]).unwrap();
    for side in [Side::Buy, Side::Sell] {
        let request = request(side, "10", false);
        assert_eq!(
            forward.quote(request).unwrap(),
            reverse.quote(request).unwrap()
        );
        assert_eq!(
            forward.quote(request).unwrap().best().unwrap().maker_id,
            "MM1"
        );
    }
}

#[test]
fn zero_requests_are_rejected_in_all_four_modes() {
    let market = Market::demo().unwrap();
    for side in [Side::Buy, Side::Sell] {
        for in_cash in [false, true] {
            assert_eq!(
                market.quote(request(side, "0", in_cash)),
                Err(MarketError::ZeroAmount)
            );
        }
    }
}

#[test]
fn a_sell_that_would_pay_zero_cash_is_rejected() {
    let market = Market::new(reference("0.000001"), vec![maker("MM1", "1", "1")]).unwrap();
    let MarketError::NoExecutableQuote(rejections) = market
        .quote(request(Side::Sell, "0.000001", false))
        .unwrap_err()
    else {
        panic!("expected rejection")
    };
    assert_eq!(rejections[0].reason, RejectionReason::BelowMinimumAmount);
}

#[test]
fn overflowing_quote_amounts_are_errors_not_wrapping_values() {
    let mm = MarketMaker {
        inventory: Inventory {
            tbill: Amount::from_atoms(u64::MAX),
            cash: Amount::from_atoms(u64::MAX),
        },
        ..maker("MM1", "1", "1")
    };
    for (price, mode) in [
        ("2", RequestAmount::Tbill(Amount::from_atoms(u64::MAX))),
        (
            "0.000001",
            RequestAmount::Cash(Amount::from_atoms(u64::MAX)),
        ),
    ] {
        let market = Market::new(reference(price), vec![mm.clone()]).unwrap();
        for side in [Side::Buy, Side::Sell] {
            let MarketError::NoExecutableQuote(rejections) = market
                .quote(QuoteRequest { side, amount: mode })
                .unwrap_err()
            else {
                panic!("expected rejection")
            };
            assert_eq!(rejections[0].reason, RejectionReason::AmountOutOfRange);
        }
    }
}

#[test]
fn invalid_market_snapshots_fail_at_construction() {
    let valid = maker("MM1", "1", "10");
    assert!(Market::new(reference("1"), Vec::new()).is_err());
    assert!(Market::new(reference("1"), vec![valid.clone(), valid.clone()]).is_err());
    for invalid in [
        MarketMaker {
            id: " ".to_owned(),
            ..valid.clone()
        },
        MarketMaker {
            target_tbill: Amount::ZERO,
            ..valid.clone()
        },
        MarketMaker {
            reserved: Inventory {
                tbill: amount("2"),
                cash: Amount::ZERO,
            },
            ..valid.clone()
        },
        MarketMaker {
            reserved: Inventory {
                cash: amount("11"),
                tbill: Amount::ZERO,
            },
            ..valid.clone()
        },
        MarketMaker {
            spread: amount("2"),
            ..valid.clone()
        },
        MarketMaker {
            inventory_skew: amount("2"),
            inventory: Inventory {
                tbill: amount("2"),
                cash: amount("10"),
            },
            ..valid.clone()
        },
    ] {
        assert!(matches!(
            Market::new(reference("1"), vec![invalid]),
            Err(MarketError::InvalidConfiguration(_))
        ));
    }
    let extreme = MarketMaker {
        inventory_skew: Amount::from_atoms(u64::MAX),
        inventory: Inventory {
            tbill: Amount::from_atoms(u64::MAX),
            cash: Amount::ZERO,
        },
        target_tbill: Amount::from_atoms(1),
        ..valid
    };
    assert_eq!(
        Market::new(reference("1"), vec![extreme]),
        Err(MarketError::ArithmeticOverflow)
    );
}

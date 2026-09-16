use rfq_dvp_market_engine::domain::{Market, MarketError, QuoteRequest, RequestAmount, Side};

fn main() -> Result<(), MarketError> {
    let market = Market::demo()?;
    println!(
        "Prix de référence : {} sEUR / tBILL",
        market.reference_price()
    );
    for (side, input) in [
        (Side::Buy, RequestAmount::Tbill("12.5".parse()?)),
        (Side::Sell, RequestAmount::Tbill("12.5".parse()?)),
        (Side::Buy, RequestAmount::Cash("1000".parse()?)),
        (Side::Sell, RequestAmount::Cash("1000".parse()?)),
    ] {
        println!("\n{side:?}, saisie {input:?}");
        let book = market.quote(QuoteRequest {
            side,
            amount: input,
        })?;
        for quote in &book.quotes {
            println!(
                "{} : prix {}, quantité {} tBILL, cash {} sEUR",
                quote.maker_id, quote.unit_price, quote.quantity_tbill, quote.cash_amount
            );
        }
        if let Some(best) = book.best() {
            println!("Meilleure quote : {}", best.maker_id);
        }
    }
    Ok(())
}

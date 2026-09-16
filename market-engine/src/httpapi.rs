use std::sync::Arc;

use crate::domain::{
    MakerRejection, Market, MarketError, QuoteRequest, RejectionReason, RequestAmount, Side,
};
use axum::{
    Json, Router,
    extract::{DefaultBodyLimit, State, rejection::JsonRejection},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::post,
};
use serde::{Deserialize, Serialize};

pub fn router(market: Market) -> Router {
    Router::new()
        .route("/quotes", post(quotes))
        .layer(DefaultBodyLimit::max(4096))
        .with_state(Arc::new(market))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    side: Direction,
    unit: Unit,
    amount: String,
}
#[derive(Deserialize)]
enum Direction {
    #[serde(rename = "BUY")]
    Buy,
    #[serde(rename = "SELL")]
    Sell,
}
#[derive(Deserialize)]
enum Unit {
    #[serde(rename = "tBILL")]
    Tbill,
    #[serde(rename = "sEUR")]
    Cash,
}

#[derive(Serialize)]
struct Quote {
    maker_id: String,
    side: &'static str,
    unit_price: String,
    quantity_tbill: String,
    cash_amount: String,
}
#[derive(Serialize)]
struct Rejection {
    maker_id: String,
    code: &'static str,
    message: String,
}
#[derive(Serialize)]
struct Book {
    quotes: Vec<Quote>,
    rejections: Vec<Rejection>,
}
#[derive(Serialize)]
struct Error {
    code: &'static str,
    message: String,
    rejections: Vec<Rejection>,
}

fn rejections(items: Vec<MakerRejection>) -> Vec<Rejection> {
    items.into_iter().map(|item| {
        let (code, message) = match item.reason {
            RejectionReason::BelowMinimumAmount => ("BELOW_MINIMUM_AMOUNT", "Montant arrondi à zéro.".to_owned()),
            RejectionReason::AmountOutOfRange => ("AMOUNT_OUT_OF_RANGE", "Montant hors limites.".to_owned()),
            RejectionReason::InsufficientInventory { asset, required, available } => ("INSUFFICIENT_INVENTORY", format!("Inventaire {asset:?} insuffisant : {required} requis, {available} disponible.")),
        };
        Rejection { maker_id: item.maker_id, code, message }
    }).collect()
}

fn error(
    status: StatusCode,
    code: &'static str,
    message: String,
    rejected: Vec<MakerRejection>,
) -> Response {
    (
        status,
        Json(Error {
            code,
            message,
            rejections: rejections(rejected),
        }),
    )
        .into_response()
}

async fn quotes(
    State(market): State<Arc<Market>>,
    payload: Result<Json<Request>, JsonRejection>,
) -> Response {
    let request =
        match payload {
            Ok(Json(request)) => request,
            Err(rejection) => return error(
                rejection.status(),
                "INVALID_REQUEST",
                "Corps JSON invalide : side BUY/SELL, unit tBILL/sEUR et amount en chaîne requis."
                    .into(),
                vec![],
            ),
        };
    let amount = match request.amount.parse() {
        Ok(amount) => amount,
        Err(err) => {
            return error(
                StatusCode::BAD_REQUEST,
                "INVALID_AMOUNT",
                format!("{err}"),
                vec![],
            );
        }
    };
    let side = match request.side {
        Direction::Buy => Side::Buy,
        Direction::Sell => Side::Sell,
    };
    let amount = match request.unit {
        Unit::Tbill => RequestAmount::Tbill(amount),
        Unit::Cash => RequestAmount::Cash(amount),
    };
    match market.quote(QuoteRequest { side, amount }) {
        Ok(book) => Json(Book {
            quotes: book
                .quotes
                .into_iter()
                .map(|q| Quote {
                    maker_id: q.maker_id,
                    side: match q.side {
                        Side::Buy => "BUY",
                        Side::Sell => "SELL",
                    },
                    unit_price: q.unit_price.to_string(),
                    quantity_tbill: q.quantity_tbill.to_string(),
                    cash_amount: q.cash_amount.to_string(),
                })
                .collect(),
            rejections: rejections(book.rejections),
        })
        .into_response(),
        Err(MarketError::NoExecutableQuote(items)) => error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "NO_EXECUTABLE_QUOTE",
            "Aucun MM ne peut servir la demande entière.".into(),
            items,
        ),
        Err(err) => error(
            StatusCode::BAD_REQUEST,
            "INVALID_AMOUNT",
            err.to_string(),
            vec![],
        ),
    }
}

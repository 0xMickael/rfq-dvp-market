use crate::{
    domain::{
        self, Engine, EngineError, MarketError, QuoteRequest, RequestAmount, RfqStatus, Side,
    },
    httpapi::{Direction, Quote, Rejection, Unit, error, rejections},
};
use axum::{
    Json, Router,
    extract::{DefaultBodyLimit, Path, State, rejection::JsonRejection},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde::{Deserialize, Serialize};
use std::sync::Arc;

pub fn router(engine: Arc<Engine>) -> Router {
    Router::new()
        .route("/market", get(market))
        .route("/trades/pending", get(pending))
        .route("/rfqs", post(create))
        .route("/rfqs/{rfq_id}", get(read))
        .route("/rfqs/{rfq_id}/accept", post(accept))
        .route("/rfqs/{rfq_id}/settlement", post(settlement))
        .layer(DefaultBodyLimit::max(4096))
        .with_state(engine)
}

async fn pending(State(engine): State<Arc<Engine>>) -> Response {
    match crate::blocking(move || engine.pending_trades()).await {
        Ok(trades) => Json(trades.into_iter().map(Trade::from).collect::<Vec<_>>()).into_response(),
        Err(e) => engine_error(e),
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CreateRequest {
    trader_id: String,
    side: Direction,
    unit: Unit,
    amount: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AcceptRequest {
    trader_id: String,
    quote_id: String,
    trade_id: String,
}

#[derive(Serialize)]
struct IdentifiedQuote {
    quote_id: String,
    trade_id: String,
    #[serde(flatten)]
    terms: Quote,
}
impl From<domain::IdentifiedQuote> for IdentifiedQuote {
    fn from(q: domain::IdentifiedQuote) -> Self {
        Self {
            quote_id: q.quote_id,
            trade_id: q.trade_id,
            terms: q.terms.into(),
        }
    }
}
#[derive(Serialize)]
struct Trade {
    transaction_hash: Option<String>,
    trade_id: String,
    rfq_id: String,
    trader_id: String,
    status: &'static str,
    settlement_authorized: bool,
    quote: IdentifiedQuote,
    accepted_at: u64,
    quote_expires_at: u64,
    settlement_deadline: u64,
    reserved_asset: &'static str,
    reserved_amount: String,
}
impl From<domain::Trade> for Trade {
    fn from(t: domain::Trade) -> Self {
        Self {
            trade_id: t.trade_id,
            rfq_id: t.rfq_id,
            trader_id: t.trader_id,
            status: match t.status {
                domain::TradeStatus::Reserved => "RESERVED",
                domain::TradeStatus::SettlementPending => "SETTLEMENT_PENDING",
                domain::TradeStatus::Settled => "SETTLED",
                domain::TradeStatus::SettlementFailed => "SETTLEMENT_FAILED",
            },
            transaction_hash: t.transaction_hash,
            settlement_authorized: false,
            quote: t.quote.into(),
            accepted_at: t.accepted_at,
            quote_expires_at: t.quote_expires_at,
            settlement_deadline: t.settlement_deadline,
            reserved_asset: match t.reserved_asset {
                domain::Asset::Tbill => "tBILL",
                domain::Asset::Cash => "sEUR",
            },
            reserved_amount: t.reserved_amount.to_string(),
        }
    }
}
#[derive(Serialize)]
struct Rfq {
    rfq_id: String,
    trader_id: String,
    status: &'static str,
    created_at: u64,
    quote_expires_at: u64,
    settlement_deadline: u64,
    quotes: Vec<IdentifiedQuote>,
    rejections: Vec<Rejection>,
    trade: Option<Trade>,
}
impl From<domain::Rfq> for Rfq {
    fn from(r: domain::Rfq) -> Self {
        Self {
            rfq_id: r.rfq_id,
            trader_id: r.trader_id,
            status: match r.status {
                RfqStatus::Quoted => "QUOTED",
                RfqStatus::Accepted => "ACCEPTED",
                RfqStatus::Expired => "EXPIRED",
            },
            created_at: r.created_at,
            quote_expires_at: r.quote_expires_at,
            settlement_deadline: r.settlement_deadline,
            quotes: r.quotes.into_iter().map(Into::into).collect(),
            rejections: rejections(r.rejections),
            trade: r.trade.map(Into::into),
        }
    }
}

fn invalid_json(rejection: JsonRejection) -> Response {
    error(
        rejection.status(),
        "INVALID_REQUEST",
        "Invalid JSON body or unknown fields.".into(),
        vec![],
    )
}

async fn create(
    State(engine): State<Arc<Engine>>,
    payload: Result<Json<CreateRequest>, JsonRejection>,
) -> Response {
    let r = match payload {
        Ok(Json(r)) => r,
        Err(e) => return invalid_json(e),
    };
    let amount = match r.amount.parse() {
        Ok(a) => a,
        Err(e) => return engine_error(EngineError::Market(e)),
    };
    let request = QuoteRequest {
        side: match r.side {
            Direction::Buy => Side::Buy,
            Direction::Sell => Side::Sell,
        },
        amount: match r.unit {
            Unit::Tbill => RequestAmount::Tbill(amount),
            Unit::Cash => RequestAmount::Cash(amount),
        },
    };
    match crate::blocking(move || engine.create_rfq(&r.trader_id, request)).await {
        Ok(r) => (StatusCode::CREATED, Json(Rfq::from(r))).into_response(),
        Err(e) => engine_error(e),
    }
}
async fn read(State(engine): State<Arc<Engine>>, Path(id): Path<String>) -> Response {
    match crate::blocking(move || engine.rfq(&id)).await {
        Ok(r) => Json(Rfq::from(r)).into_response(),
        Err(e) => engine_error(e),
    }
}
async fn accept(
    State(engine): State<Arc<Engine>>,
    Path(id): Path<String>,
    payload: Result<Json<AcceptRequest>, JsonRejection>,
) -> Response {
    let r = match payload {
        Ok(Json(r)) => r,
        Err(e) => return invalid_json(e),
    };
    match crate::blocking(move || {
        engine.accept_expected(&id, &r.quote_id, &r.trader_id, Some(&r.trade_id))
    })
    .await
    {
        Ok(t) => Json(Trade::from(t)).into_response(),
        Err(e) => engine_error(e),
    }
}

pub(super) fn engine_error(e: EngineError) -> Response {
    let (status, code, message) = match e {
        EngineError::InvalidTransition => (
            StatusCode::CONFLICT,
            "INVALID_TRANSITION",
            "Invalid settlement transition.",
        ),
        EngineError::Market(MarketError::NoExecutableQuote(items)) => {
            return error(
                StatusCode::UNPROCESSABLE_ENTITY,
                "NO_EXECUTABLE_QUOTE",
                "No market maker can fill the entire request.".into(),
                items,
            );
        }
        EngineError::Market(e) => {
            return error(
                StatusCode::BAD_REQUEST,
                "INVALID_AMOUNT",
                e.to_string(),
                vec![],
            );
        }
        EngineError::InvalidTrader => (
            StatusCode::BAD_REQUEST,
            "INVALID_TRADER",
            "Declared identity required: 1 to 64 ASCII letters, digits, _ or -.",
        ),
        EngineError::UnknownRfq => (StatusCode::NOT_FOUND, "UNKNOWN_RFQ", "Unknown RFQ."),
        EngineError::WrongTrader => (
            StatusCode::FORBIDDEN,
            "WRONG_TRADER",
            "Identity does not match the RFQ; this check is not authentication.",
        ),
        EngineError::UnknownQuote => (
            StatusCode::NOT_FOUND,
            "UNKNOWN_QUOTE",
            "Unknown quote for this RFQ.",
        ),
        EngineError::WrongTradeId => (
            StatusCode::CONFLICT,
            "WRONG_TRADE_ID",
            "Signed trade ID does not match the quote.",
        ),
        EngineError::AlreadyAccepted => (
            StatusCode::CONFLICT,
            "ALREADY_ACCEPTED",
            "RFQ already accepted; read the RFQ to retrieve its trade.",
        ),
        EngineError::Expired => (
            StatusCode::GONE,
            "QUOTE_EXPIRED",
            "Quote acceptance deadline reached.",
        ),
        EngineError::InsufficientInventory { .. } => (
            StatusCode::CONFLICT,
            "INSUFFICIENT_INVENTORY",
            "Maker inventory is no longer sufficient; no reservation created.",
        ),
        EngineError::ExhaustedIds | EngineError::Unavailable => (
            StatusCode::SERVICE_UNAVAILABLE,
            "ENGINE_UNAVAILABLE",
            "Market engine unavailable.",
        ),
    };
    error(status, code, message.into(), vec![])
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SettlementRequest {
    trade_id: String,
    status: String,
    transaction_hash: Option<String>,
    block_timestamp: u64,
}
async fn settlement(
    State(engine): State<Arc<Engine>>,
    Path(id): Path<String>,
    payload: Result<Json<SettlementRequest>, JsonRejection>,
) -> Response {
    let r = match payload {
        Ok(Json(r)) => r,
        Err(e) => return invalid_json(e),
    };
    let status = match r.status.as_str() {
        "SETTLEMENT_PENDING" => domain::TradeStatus::SettlementPending,
        "SETTLED" => domain::TradeStatus::Settled,
        "SETTLEMENT_FAILED" => domain::TradeStatus::SettlementFailed,
        _ => return engine_error(EngineError::InvalidTransition),
    };
    match crate::blocking(move || {
        engine.settlement(
            &id,
            &r.trade_id,
            status,
            r.transaction_hash,
            r.block_timestamp,
        )
    })
    .await
    {
        Ok(t) => Json(Trade::from(t)).into_response(),
        Err(e) => engine_error(e),
    }
}

#[derive(Serialize)]
struct MakerView {
    id: String,
    bid: String,
    ask: String,
    tbill: String,
    cash: String,
    reserved_tbill: String,
    reserved_cash: String,
    available_tbill: String,
    available_cash: String,
}
#[derive(Serialize)]
struct MarketView {
    reference_price: String,
    makers: Vec<MakerView>,
}
async fn market(State(engine): State<Arc<Engine>>) -> Response {
    let snapshot = match crate::blocking(move || engine.market_snapshot()).await {
        Ok(v) => v,
        Err(_) => return StatusCode::SERVICE_UNAVAILABLE.into_response(),
    };
    let mut makers = Vec::new();
    for m in snapshot.makers() {
        let (Ok(p), Ok(a)) = (m.prices(snapshot.reference_price()), m.available()) else {
            return StatusCode::SERVICE_UNAVAILABLE.into_response();
        };
        makers.push(MakerView {
            id: m.id.clone(),
            bid: p.bid.to_string(),
            ask: p.ask.to_string(),
            tbill: m.inventory.tbill.to_string(),
            cash: m.inventory.cash.to_string(),
            reserved_tbill: m.reserved.tbill.to_string(),
            reserved_cash: m.reserved.cash.to_string(),
            available_tbill: a.tbill.to_string(),
            available_cash: a.cash.to_string(),
        });
    }
    Json(MarketView {
        reference_price: snapshot.reference_price().to_string(),
        makers,
    })
    .into_response()
}

//! Exact quoting, inventory reservations and market state transitions.

mod amount;
mod engine;
mod pricing;
mod quote;

pub use amount::{Amount, SCALE};
pub use engine::{
    Engine, EngineError, IdentifiedQuote, Rfq, RfqStatus, Timing, Trade, TradeStatus,
};
pub use pricing::{Inventory, MarketMaker, Prices, ReferenceMarket};
pub use quote::{Market, Quote, QuoteBook, QuoteRequest, RequestAmount, Side};

#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub enum Asset {
    Tbill,
    Cash,
}

#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub enum RejectionReason {
    BelowMinimumAmount,
    AmountOutOfRange,
    InsufficientInventory {
        asset: Asset,
        required: Amount,
        available: Amount,
    },
}

#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct MakerRejection {
    pub maker_id: String,
    pub reason: RejectionReason,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MarketError {
    InvalidDecimal,
    TooManyDecimals,
    ArithmeticOverflow,
    ZeroAmount,
    InvalidConfiguration(&'static str),
    NoExecutableQuote(Vec<MakerRejection>),
}

impl std::fmt::Display for MarketError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidDecimal => write!(f, "invalid decimal amount"),
            Self::TooManyDecimals => write!(f, "at most six decimal places"),
            Self::ArithmeticOverflow => write!(f, "amount or arithmetic out of range"),
            Self::ZeroAmount => write!(f, "requested amount must be positive"),
            Self::InvalidConfiguration(reason) => write!(f, "invalid configuration: {reason}"),
            Self::NoExecutableQuote(rejections) => {
                write!(f, "no market maker can fill the request: {rejections:?}")
            }
        }
    }
}

impl std::error::Error for MarketError {}

//! Cotation pure et moteur de réservation en mémoire, indépendants du réseau.

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
            Self::InvalidDecimal => write!(f, "montant décimal invalide"),
            Self::TooManyDecimals => write!(f, "six décimales au maximum"),
            Self::ArithmeticOverflow => write!(f, "montant ou calcul hors limites"),
            Self::ZeroAmount => write!(f, "le montant demandé doit être strictement positif"),
            Self::InvalidConfiguration(reason) => write!(f, "configuration invalide : {reason}"),
            Self::NoExecutableQuote(rejections) => {
                write!(
                    f,
                    "aucun market maker ne peut servir la demande : {rejections:?}"
                )
            }
        }
    }
}

impl std::error::Error for MarketError {}

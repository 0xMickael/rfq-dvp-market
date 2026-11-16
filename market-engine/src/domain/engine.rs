use std::{collections::HashMap, sync::Mutex};

use super::{
    Amount, Asset, Inventory, MakerRejection, Market, MarketError, Quote, QuoteRequest, Side,
};

#[derive(Debug, Clone, Copy)]
pub struct Timing {
    pub quote_seconds: u64,
    pub settlement_seconds: u64,
}

impl Default for Timing {
    fn default() -> Self {
        Self {
            quote_seconds: 30,
            settlement_seconds: 300,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct IdentifiedQuote {
    pub quote_id: String,
    pub trade_id: String,
    pub terms: Quote,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RfqStatus {
    Quoted,
    Accepted,
    Expired,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Rfq {
    pub rfq_id: String,
    /// Identité déclarative locale, sans preuve de contrôle d'un wallet.
    pub trader_id: String,
    pub request: QuoteRequest,
    pub created_at: u64,
    pub quote_expires_at: u64,
    pub settlement_deadline: u64,
    pub status: RfqStatus,
    pub quotes: Vec<IdentifiedQuote>,
    pub rejections: Vec<MakerRejection>,
    pub trade: Option<Trade>,
}

/// Uniquement une réservation interne. Aucune signature ni autorisation on-chain.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Trade {
    pub status: TradeStatus,
    pub transaction_hash: Option<String>,
    pub trade_id: String,
    pub rfq_id: String,
    pub trader_id: String,
    pub quote: IdentifiedQuote,
    pub accepted_at: u64,
    pub quote_expires_at: u64,
    pub settlement_deadline: u64,
    pub reserved_asset: Asset,
    pub reserved_amount: Amount,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TradeStatus {
    Reserved,
    SettlementPending,
    Settled,
    SettlementFailed,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum EngineError {
    Market(MarketError),
    InvalidTrader,
    UnknownRfq,
    WrongTrader,
    UnknownQuote,
    WrongTradeId,
    AlreadyAccepted,
    Expired,
    InvalidTransition,
    InsufficientInventory {
        asset: Asset,
        required: Amount,
        available: Amount,
    },
    ExhaustedIds,
    Unavailable,
}

impl From<MarketError> for EngineError {
    fn from(value: MarketError) -> Self {
        Self::Market(value)
    }
}

struct State {
    market: Market,
    rfqs: HashMap<String, Rfq>,
    next_id: u64,
    last_now: u64,
    session: String,
}

/// Un verrou protège ensemble RFQs, trades et inventaires ; aucun I/O sous ce verrou.
pub struct Engine {
    state: Mutex<State>,
    timing: Timing,
    clock: Box<dyn Fn() -> u64 + Send + Sync>,
}

impl Engine {
    pub fn new(
        market: Market,
        timing: Timing,
        clock: impl Fn() -> u64 + Send + Sync + 'static,
    ) -> Result<Self, EngineError> {
        if timing.quote_seconds == 0 || timing.settlement_seconds <= timing.quote_seconds {
            return Err(MarketError::InvalidConfiguration("échéances invalides").into());
        }
        let mut nonce = [0u8; 16];
        getrandom::fill(&mut nonce).map_err(|_| EngineError::Unavailable)?;
        let session = u128::from_be_bytes(nonce);
        Ok(Self {
            state: Mutex::new(State {
                market,
                rfqs: HashMap::new(),
                next_id: 1,
                last_now: 0,
                session: format!("{session:032x}"),
            }),
            timing,
            clock: Box::new(clock),
        })
    }

    // Échantillonné après acquisition du verrou. Un recul de l'horloge ne rouvre pas une quote.
    fn now(&self, state: &mut State) -> u64 {
        state.last_now = state.last_now.max((self.clock)());
        state.last_now
    }

    pub fn market_snapshot(&self) -> Result<Market, EngineError> {
        let state = self.state.lock().map_err(|_| EngineError::Unavailable)?;
        Ok(state.market.clone())
    }

    pub fn indicative_quote(&self, request: QuoteRequest) -> Result<super::QuoteBook, EngineError> {
        let state = self.state.lock().map_err(|_| EngineError::Unavailable)?;
        Ok(state.market.quote(request)?)
    }

    pub fn create_rfq(&self, trader_id: &str, request: QuoteRequest) -> Result<Rfq, EngineError> {
        if trader_id.is_empty()
            || trader_id.len() > 64
            || !trader_id
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || b"_-".contains(&c))
        {
            return Err(EngineError::InvalidTrader);
        }
        let mut state = self.state.lock().map_err(|_| EngineError::Unavailable)?;
        let now = self.now(&mut state);
        let quote_expires_at = now
            .checked_add(self.timing.quote_seconds)
            .ok_or(MarketError::ArithmeticOverflow)?;
        let settlement_deadline = now
            .checked_add(self.timing.settlement_seconds)
            .ok_or(MarketError::ArithmeticOverflow)?;
        let book = state.market.quote(request)?;
        let next_id = state
            .next_id
            .checked_add(1 + book.quotes.len() as u64)
            .ok_or(EngineError::ExhaustedIds)?;
        let rfq = Rfq {
            rfq_id: format!("rfq-{}", state.next_id),
            trader_id: trader_id.into(),
            request,
            created_at: now,
            quote_expires_at,
            settlement_deadline,
            status: RfqStatus::Quoted,
            quotes: book
                .quotes
                .into_iter()
                .enumerate()
                .map(|(index, terms)| IdentifiedQuote {
                    quote_id: format!("quote-{}", state.next_id + 1 + index as u64),
                    trade_id: format!(
                        "0x{}{:032x}",
                        state.session,
                        state.next_id + 1 + index as u64
                    ),
                    terms,
                })
                .collect(),
            rejections: book.rejections,
            trade: None,
        };
        state.next_id = next_id;
        state.rfqs.insert(rfq.rfq_id.clone(), rfq.clone());
        Ok(rfq)
    }

    pub fn rfq(&self, rfq_id: &str) -> Result<Rfq, EngineError> {
        let mut state = self.state.lock().map_err(|_| EngineError::Unavailable)?;
        let now = self.now(&mut state);
        let rfq = state.rfqs.get_mut(rfq_id).ok_or(EngineError::UnknownRfq)?;
        expire(rfq, now);
        Ok(rfq.clone())
    }

    pub fn maker_inventory(
        &self,
        maker_id: &str,
    ) -> Result<Option<(Inventory, Inventory)>, EngineError> {
        let state = self.state.lock().map_err(|_| EngineError::Unavailable)?;
        Ok(state
            .market
            .makers
            .iter()
            .find(|m| m.id == maker_id)
            .map(|m| (m.inventory, m.reserved)))
    }

    pub fn accept(
        &self,
        rfq_id: &str,
        quote_id: &str,
        trader_id: &str,
    ) -> Result<Trade, EngineError> {
        self.accept_expected(rfq_id, quote_id, trader_id, None)
    }

    pub fn accept_expected(
        &self,
        rfq_id: &str,
        quote_id: &str,
        trader_id: &str,
        trade_id: Option<&str>,
    ) -> Result<Trade, EngineError> {
        let mut state = self.state.lock().map_err(|_| EngineError::Unavailable)?;
        let now = self.now(&mut state);
        let rfq = state.rfqs.get_mut(rfq_id).ok_or(EngineError::UnknownRfq)?;
        if rfq.trader_id != trader_id {
            return Err(EngineError::WrongTrader);
        }
        let quote = rfq
            .quotes
            .iter()
            .find(|q| q.quote_id == quote_id)
            .cloned()
            .ok_or(EngineError::UnknownQuote)?;
        if trade_id.is_some_and(|expected| expected != quote.trade_id) {
            return Err(EngineError::WrongTradeId);
        }
        expire(rfq, now);
        match rfq.status {
            RfqStatus::Accepted => return Err(EngineError::AlreadyAccepted),
            RfqStatus::Expired => return Err(EngineError::Expired),
            RfqStatus::Quoted => (),
        }
        let rfq = rfq.clone();
        let maker_index = state
            .market
            .makers
            .iter()
            .position(|m| m.id == quote.terms.maker_id)
            .ok_or(EngineError::Unavailable)?;
        let maker = &state.market.makers[maker_index];
        let available = maker.available()?;
        let (asset, required, available, reserved) = match quote.terms.side {
            Side::Buy => (
                Asset::Tbill,
                quote.terms.quantity_tbill,
                available.tbill,
                maker.reserved.tbill,
            ),
            Side::Sell => (
                Asset::Cash,
                quote.terms.cash_amount,
                available.cash,
                maker.reserved.cash,
            ),
        };
        if required > available {
            return Err(EngineError::InsufficientInventory {
                asset,
                required,
                available,
            });
        }
        let new_reserved = reserved
            .atoms()
            .checked_add(required.atoms())
            .ok_or(MarketError::ArithmeticOverflow)?;
        let trade = Trade {
            status: TradeStatus::Reserved,
            transaction_hash: None,
            trade_id: quote.trade_id.clone(),
            rfq_id: rfq.rfq_id.clone(),
            trader_id: rfq.trader_id,
            quote,
            accepted_at: now,
            quote_expires_at: rfq.quote_expires_at,
            settlement_deadline: rfq.settlement_deadline,
            reserved_asset: asset,
            reserved_amount: required,
        };
        // Tous les refus possibles précèdent ces écritures, invisibles avant libération du verrou.
        let maker = &mut state.market.makers[maker_index];
        match asset {
            Asset::Tbill => maker.reserved.tbill = Amount::from_atoms(new_reserved),
            Asset::Cash => maker.reserved.cash = Amount::from_atoms(new_reserved),
        }
        let stored = state
            .rfqs
            .get_mut(rfq_id)
            .expect("RFQ vérifiée sous le même verrou");
        stored.status = RfqStatus::Accepted;
        stored.trade = Some(trade.clone());
        Ok(trade)
    }

    // Go vérifie la preuve RPC ; cette route interne lui fait confiance.
    pub fn settlement(
        &self,
        rfq_id: &str,
        trade_id: &str,
        status: TradeStatus,
        transaction_hash: Option<String>,
        block_timestamp: u64,
    ) -> Result<Trade, EngineError> {
        let mut state = self.state.lock().map_err(|_| EngineError::Unavailable)?;
        let mut trade = state
            .rfqs
            .get(rfq_id)
            .ok_or(EngineError::UnknownRfq)?
            .trade
            .clone()
            .ok_or(EngineError::InvalidTransition)?;
        if trade.trade_id != trade_id {
            return Err(EngineError::WrongTradeId);
        }
        if status == TradeStatus::Reserved {
            return Err(EngineError::InvalidTransition);
        }
        if let Some(hash) = &transaction_hash
            && (hash.len() != 66
                || !hash.starts_with("0x")
                || !hash[2..].bytes().all(|c| c.is_ascii_hexdigit()))
        {
            return Err(EngineError::InvalidTransition);
        }
        if matches!(
            trade.status,
            TradeStatus::Settled | TradeStatus::SettlementFailed
        ) {
            return if trade.status == status {
                Ok(trade)
            } else {
                Err(EngineError::InvalidTransition)
            };
        }
        if status == TradeStatus::Settled
            && (transaction_hash.is_none() || block_timestamp >= trade.settlement_deadline)
        {
            return Err(EngineError::InvalidTransition);
        }
        if status == TradeStatus::SettlementFailed && block_timestamp < trade.settlement_deadline {
            return Err(EngineError::InvalidTransition);
        }
        if matches!(status, TradeStatus::Settled | TradeStatus::SettlementFailed) {
            let maker = state
                .market
                .makers
                .iter_mut()
                .find(|m| m.id == trade.quote.terms.maker_id)
                .ok_or(EngineError::Unavailable)?;
            let mut inventory = maker.inventory;
            let mut reserved = maker.reserved;
            let q = &trade.quote.terms;
            let sub = |a: Amount, b: Amount| {
                a.atoms()
                    .checked_sub(b.atoms())
                    .map(Amount::from_atoms)
                    .ok_or(EngineError::InvalidTransition)
            };
            let add = |a: Amount, b: Amount| {
                a.atoms()
                    .checked_add(b.atoms())
                    .map(Amount::from_atoms)
                    .ok_or(EngineError::Market(MarketError::ArithmeticOverflow))
            };
            match q.side {
                Side::Buy => {
                    reserved.tbill = sub(reserved.tbill, q.quantity_tbill)?;
                    if status == TradeStatus::Settled {
                        inventory.tbill = sub(inventory.tbill, q.quantity_tbill)?;
                        inventory.cash = add(inventory.cash, q.cash_amount)?;
                    }
                }
                Side::Sell => {
                    reserved.cash = sub(reserved.cash, q.cash_amount)?;
                    if status == TradeStatus::Settled {
                        inventory.cash = sub(inventory.cash, q.cash_amount)?;
                        inventory.tbill = add(inventory.tbill, q.quantity_tbill)?;
                    }
                }
            }
            maker.inventory = inventory;
            maker.reserved = reserved;
        }
        trade.status = status;
        if transaction_hash.is_some() {
            trade.transaction_hash = transaction_hash;
        }
        state
            .rfqs
            .get_mut(rfq_id)
            .expect("RFQ vérifiée sous verrou")
            .trade = Some(trade.clone());
        Ok(trade)
    }
}

fn expire(rfq: &mut Rfq, now: u64) {
    if rfq.status == RfqStatus::Quoted && now >= rfq.quote_expires_at {
        rfq.status = RfqStatus::Expired;
    }
}

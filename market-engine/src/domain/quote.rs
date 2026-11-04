use super::amount::{divide_up, from_wide};
use super::{
    Amount, Asset, Inventory, MakerRejection, MarketError, MarketMaker, ReferenceMarket,
    RejectionReason, SCALE,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Side {
    Buy,
    Sell,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RequestAmount {
    /// Quantité exacte de titres, dans les deux sens.
    Tbill(Amount),
    /// Budget maximal pour BUY ; cash minimal à recevoir pour SELL.
    Cash(Amount),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct QuoteRequest {
    pub side: Side,
    pub amount: RequestAmount,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Quote {
    pub maker_id: String,
    pub side: Side,
    pub unit_price: Amount,
    pub quantity_tbill: Amount,
    pub cash_amount: Amount,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct QuoteBook {
    /// Classées du meilleur prix au moins bon, puis par identifiant MM en cas d'égalité.
    pub quotes: Vec<Quote>,
    pub rejections: Vec<MakerRejection>,
}

impl QuoteBook {
    pub fn best(&self) -> Option<&Quote> {
        self.quotes.first()
    }
}

/// Snapshot immuable. Obtenir une quote ne bloque pas les fonds ni les titres.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Market {
    reference_price: Amount,
    pub(super) makers: Vec<MarketMaker>,
}

impl Market {
    pub fn new(reference: ReferenceMarket, makers: Vec<MarketMaker>) -> Result<Self, MarketError> {
        let reference_price = reference.price()?;
        if makers.is_empty() {
            return Err(MarketError::InvalidConfiguration("aucun market maker"));
        }
        for (index, maker) in makers.iter().enumerate() {
            if makers[..index]
                .iter()
                .any(|previous| previous.id == maker.id)
            {
                return Err(MarketError::InvalidConfiguration("identifiant MM dupliqué"));
            }
            maker.prices(reference_price)?;
        }
        Ok(Self {
            reference_price,
            makers,
        })
    }

    pub fn reference_price(&self) -> Amount {
        self.reference_price
    }

    pub fn quote(&self, request: QuoteRequest) -> Result<QuoteBook, MarketError> {
        let (RequestAmount::Tbill(amount) | RequestAmount::Cash(amount)) = request.amount;
        if amount == Amount::ZERO {
            return Err(MarketError::ZeroAmount);
        }
        let mut book = QuoteBook {
            quotes: Vec::new(),
            rejections: Vec::new(),
        };
        for maker in &self.makers {
            let prices = maker.prices(self.reference_price)?;
            let price = match request.side {
                Side::Buy => prices.ask,
                Side::Sell => prices.bid,
            };
            match quote_for_maker(maker, maker.available()?, price, request) {
                Ok(quote) => book.quotes.push(quote),
                Err(reason) => book.rejections.push(MakerRejection {
                    maker_id: maker.id.clone(),
                    reason,
                }),
            }
        }
        book.quotes.sort_by(|left, right| {
            let price_order = match request.side {
                Side::Buy => left.unit_price.cmp(&right.unit_price),
                Side::Sell => right.unit_price.cmp(&left.unit_price),
            };
            price_order.then_with(|| left.maker_id.cmp(&right.maker_id))
        });
        book.rejections
            .sort_by(|left, right| left.maker_id.cmp(&right.maker_id));
        if book.quotes.is_empty() {
            return Err(MarketError::NoExecutableQuote(book.rejections));
        }
        Ok(book)
    }

    pub fn demo() -> Result<Self, MarketError> {
        let mut makers = Vec::new();
        for (id, tbill, cash, spread) in [
            ("MM1", "1000", "100000", "0.08"),
            ("MM2", "1200", "90000", "0.06"),
            ("MM3", "800", "110000", "0.10"),
        ] {
            makers.push(MarketMaker {
                id: id.to_owned(),
                inventory: Inventory {
                    tbill: tbill.parse()?,
                    cash: cash.parse()?,
                },
                reserved: Inventory::default(),
                target_tbill: "1000".parse()?,
                inventory_skew: "0.1".parse()?,
                spread: spread.parse()?,
            });
        }
        Self::new(
            ReferenceMarket {
                face_value: "100".parse()?,
                annual_rate: "0.04".parse()?,
                maturity_years: "0.5".parse()?,
            },
            makers,
        )
    }
}

fn quote_for_maker(
    maker: &MarketMaker,
    available: Inventory,
    price: Amount,
    request: QuoteRequest,
) -> Result<Quote, RejectionReason> {
    let scale = u128::from(SCALE);
    let price_atoms = u128::from(price.atoms());
    let quantity = match request.amount {
        RequestAmount::Tbill(quantity) => quantity,
        RequestAmount::Cash(cash) => {
            let numerator = u128::from(cash.atoms()) * scale;
            let atoms = match request.side {
                Side::Buy => numerator / price_atoms,
                Side::Sell => divide_up(numerator, price_atoms),
            };
            from_wide(atoms).map_err(|_| RejectionReason::AmountOutOfRange)?
        }
    };
    let product = u128::from(quantity.atoms()) * price_atoms;
    let cash_atoms = match request.side {
        Side::Buy => divide_up(product, scale),
        Side::Sell => product / scale,
    };
    let cash = from_wide(cash_atoms).map_err(|_| RejectionReason::AmountOutOfRange)?;
    if quantity == Amount::ZERO || cash == Amount::ZERO {
        return Err(RejectionReason::BelowMinimumAmount);
    }
    let (asset, required, available) = match request.side {
        Side::Buy => (Asset::Tbill, quantity, available.tbill),
        Side::Sell => (Asset::Cash, cash, available.cash),
    };
    if required > available {
        return Err(RejectionReason::InsufficientInventory {
            asset,
            required,
            available,
        });
    }
    Ok(Quote {
        maker_id: maker.id.clone(),
        side: request.side,
        unit_price: price,
        quantity_tbill: quantity,
        cash_amount: cash,
    })
}

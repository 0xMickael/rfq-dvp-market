use super::amount::from_wide;
use super::{Amount, MarketError, SCALE};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ReferenceMarket {
    pub face_value: Amount,
    pub annual_rate: Amount,
    pub maturity_years: Amount,
}

impl ReferenceMarket {
    /// P_ref = FaceValue / (1 + r * tau), arrondi vers le bas à six décimales.
    pub fn price(self) -> Result<Amount, MarketError> {
        if self.face_value == Amount::ZERO {
            return Err(MarketError::InvalidConfiguration("nominal nul"));
        }
        if self.annual_rate.atoms() > SCALE || self.maturity_years.atoms() > 100 * SCALE {
            return Err(MarketError::InvalidConfiguration(
                "taux hors [0, 1] ou maturité hors [0, 100] ans",
            ));
        }
        let scale_squared = u128::from(SCALE) * u128::from(SCALE);
        let denominator = scale_squared
            + u128::from(self.annual_rate.atoms()) * u128::from(self.maturity_years.atoms());
        let price = from_wide(u128::from(self.face_value.atoms()) * scale_squared / denominator)?;
        if price == Amount::ZERO {
            return Err(MarketError::InvalidConfiguration(
                "prix de référence inférieur à un atome",
            ));
        }
        Ok(price)
    }
}

#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
pub struct Inventory {
    pub tbill: Amount,
    pub cash: Amount,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MarketMaker {
    pub id: String,
    pub inventory: Inventory,
    /// Snapshot des sorties déjà engagées. Ce module ne crée pas de réservation.
    pub reserved: Inventory,
    pub target_tbill: Amount,
    /// lambda, en sEUR / tBILL par unité de déviation relative.
    pub inventory_skew: Amount,
    /// Spread total, en sEUR / tBILL.
    pub spread: Amount,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Prices {
    pub bid: Amount,
    pub ask: Amount,
}

impl MarketMaker {
    pub fn available(&self) -> Result<Inventory, MarketError> {
        Ok(Inventory {
            tbill: self.inventory.tbill.checked_sub(self.reserved.tbill)?,
            cash: self.inventory.cash.checked_sub(self.reserved.cash)?,
        })
    }

    pub fn prices(&self, reference_price: Amount) -> Result<Prices, MarketError> {
        if self.id.trim().is_empty() || self.target_tbill == Amount::ZERO {
            return Err(MarketError::InvalidConfiguration(
                "identifiant vide ou target nul",
            ));
        }
        self.available()?;
        let deviation =
            i128::from(self.inventory.tbill.atoms()) - i128::from(self.target_tbill.atoms());
        // Une seule division : ne pas arrondir d'abord la déviation relative.
        let skew = i128::from(self.inventory_skew.atoms())
            .checked_mul(deviation)
            .ok_or(MarketError::ArithmeticOverflow)?
            / i128::from(self.target_tbill.atoms());
        let mid = i128::from(reference_price.atoms())
            .checked_sub(skew)
            .ok_or(MarketError::ArithmeticOverflow)?;
        // Si le spread a un nombre impair d'atomes, arrondir les deux prix vers l'extérieur.
        let half_spread = i128::from(self.spread.atoms().div_ceil(2));
        let bid = mid
            .checked_sub(half_spread)
            .ok_or(MarketError::ArithmeticOverflow)?;
        let ask = mid
            .checked_add(half_spread)
            .ok_or(MarketError::ArithmeticOverflow)?;
        if bid <= 0 {
            return Err(MarketError::InvalidConfiguration("bid nul ou négatif"));
        }
        Ok(Prices {
            bid: Amount::from_atoms(
                u64::try_from(bid).map_err(|_| MarketError::ArithmeticOverflow)?,
            ),
            ask: Amount::from_atoms(
                u64::try_from(ask).map_err(|_| MarketError::ArithmeticOverflow)?,
            ),
        })
    }
}

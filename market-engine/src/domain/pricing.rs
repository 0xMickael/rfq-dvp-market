use super::amount::from_wide;
use super::{Amount, MarketError, SCALE};

#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct ReferenceMarket {
    pub face_value: Amount,
    pub annual_rate: Amount,
    pub maturity_years: Amount,
}

impl ReferenceMarket {
    /// P_ref = FaceValue / (1 + r * tau), rounded down to six decimal places.
    pub fn price(self) -> Result<Amount, MarketError> {
        if self.face_value == Amount::ZERO {
            return Err(MarketError::InvalidConfiguration("zero face value"));
        }
        if self.annual_rate.atoms() > SCALE || self.maturity_years.atoms() > 100 * SCALE {
            return Err(MarketError::InvalidConfiguration(
                "rate outside [0, 1] or maturity outside [0, 100] years",
            ));
        }
        let scale_squared = u128::from(SCALE) * u128::from(SCALE);
        let denominator = scale_squared
            + u128::from(self.annual_rate.atoms()) * u128::from(self.maturity_years.atoms());
        let price = from_wide(u128::from(self.face_value.atoms()) * scale_squared / denominator)?;
        if price == Amount::ZERO {
            return Err(MarketError::InvalidConfiguration(
                "reference price is less than one atom",
            ));
        }
        Ok(price)
    }
}

#[derive(Debug, Default, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct Inventory {
    pub tbill: Amount,
    pub cash: Amount,
}

#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct MarketMaker {
    pub id: String,
    pub inventory: Inventory,
    /// Snapshot of committed outgoing assets; pricing does not create reservations.
    pub reserved: Inventory,
    pub target_tbill: Amount,
    /// lambda, in sEUR / tBILL per unit of relative inventory deviation.
    pub inventory_skew: Amount,
    /// Full spread in sEUR / tBILL.
    pub spread: Amount,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
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
                "empty maker ID or zero target",
            ));
        }
        self.available()?;
        let deviation =
            i128::from(self.inventory.tbill.atoms()) - i128::from(self.target_tbill.atoms());
        // Divide once, without rounding the relative deviation first.
        let skew = i128::from(self.inventory_skew.atoms())
            .checked_mul(deviation)
            .ok_or(MarketError::ArithmeticOverflow)?
            / i128::from(self.target_tbill.atoms());
        let mid = i128::from(reference_price.atoms())
            .checked_sub(skew)
            .ok_or(MarketError::ArithmeticOverflow)?;
        // For an odd spread in atoms, round both prices outward.
        let half_spread = i128::from(self.spread.atoms().div_ceil(2));
        let bid = mid
            .checked_sub(half_spread)
            .ok_or(MarketError::ArithmeticOverflow)?;
        let ask = mid
            .checked_add(half_spread)
            .ok_or(MarketError::ArithmeticOverflow)?;
        if bid <= 0 {
            return Err(MarketError::InvalidConfiguration("zero or negative bid"));
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

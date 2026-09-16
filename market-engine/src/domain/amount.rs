use super::MarketError;

pub const SCALE: u64 = 1_000_000;

/// Valeur non négative à six décimales. L'unité est indiquée par le champ métier.
/// Le stockage u64 est élargi en u128 pour les produits de deux montants.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct Amount(u64);

impl Amount {
    pub const ZERO: Self = Self(0);

    pub const fn from_atoms(atoms: u64) -> Self {
        Self(atoms)
    }

    pub const fn atoms(self) -> u64 {
        self.0
    }

    pub fn checked_sub(self, other: Self) -> Result<Self, MarketError> {
        self.0
            .checked_sub(other.0)
            .map(Self)
            .ok_or(MarketError::InvalidConfiguration(
                "réservation supérieure à l'inventaire",
            ))
    }
}

impl std::str::FromStr for Amount {
    type Err = MarketError;

    fn from_str(input: &str) -> Result<Self, Self::Err> {
        let (whole, fraction) = match input.split_once('.') {
            Some((whole, fraction)) if !fraction.is_empty() => (whole, fraction),
            Some(_) => return Err(MarketError::InvalidDecimal),
            None => (input, ""),
        };
        if whole.is_empty()
            || !whole.bytes().all(|c| c.is_ascii_digit())
            || !fraction.bytes().all(|c| c.is_ascii_digit())
        {
            return Err(MarketError::InvalidDecimal);
        }
        if fraction.len() > 6 {
            return Err(MarketError::TooManyDecimals);
        }
        let whole: u64 = whole.parse().map_err(|_| MarketError::ArithmeticOverflow)?;
        let fraction = if fraction.is_empty() {
            0
        } else {
            let digits: u64 = fraction.parse().map_err(|_| MarketError::InvalidDecimal)?;
            digits * 10_u64.pow(6 - fraction.len() as u32)
        };
        whole
            .checked_mul(SCALE)
            .and_then(|atoms| atoms.checked_add(fraction))
            .map(Self)
            .ok_or(MarketError::ArithmeticOverflow)
    }
}

impl std::fmt::Display for Amount {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}.{:06}", self.0 / SCALE, self.0 % SCALE)
    }
}

pub(super) fn from_wide(atoms: u128) -> Result<Amount, MarketError> {
    u64::try_from(atoms)
        .map(Amount::from_atoms)
        .map_err(|_| MarketError::ArithmeticOverflow)
}

/// Le quotient + reste évite l'overflow de la formule (n + d - 1) / d.
pub(super) fn divide_up(numerator: u128, denominator: u128) -> u128 {
    numerator / denominator + u128::from(!numerator.is_multiple_of(denominator))
}

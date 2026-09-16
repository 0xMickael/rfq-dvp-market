# Contrats

SEUR et TBILL sont des ERC-20 à six décimales, avec mint réservé à leur propriétaire issuer. DvPSettlement vérifie deux signatures EIP-712, la deadline et le replay, puis échange les actifs atomiquement.

Voir [settlement.md](../../docs/settlement.md) pour le protocole, les hypothèses et les validations. Aucun proxy, fonds ERC-4626 ou remboursement à maturité.

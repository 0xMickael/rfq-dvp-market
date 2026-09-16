# Tests Solidity

Depuis contracts : installer les dépendances avec `./install-deps.ps1`, puis lancer `forge test -vv`.

Les tests couvrent les montants BUY/SELL, les permissions, la fixture Cast EIP-712, les signatures, le replay, la deadline et le rollback. Deux tests fuzz couvrent les montants variables et les termes altérés. Le test sur Anvil réel est `../scripts/test-settlement.ps1`.

Voir [settlement.md](../../docs/settlement.md).

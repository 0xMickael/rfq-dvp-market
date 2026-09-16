# Contrats et settlement signé

## Disponible au 17 septembre 2026

`SEUR`, `TBILL` et `DvPSettlement` sont compilés et testés avec Solidity 0.8.30 / Cancun. Les scripts Foundry déploient, financent et exécutent BUY/SELL sur Anvil. Le frontend et Go ne soumettent pas encore de settlement ; Rust ne réserve pas encore les inventaires.

## Actifs

Deux ERC-20 OpenZeppelin à six décimales. `mint(to, amount)` est réservé au propriétaire issuer (`Ownable`). Les méthodes ERC-20 et de gestion du propriétaire sont héritées. Aucun remboursement à maturité, yield, frais de transfert ou rebase. Le déploiement local utilise le même issuer pour les deux tokens ; leurs propriétaires peuvent être distincts.

## Termes EIP-712

Domaine : `name = rfq-dvp-market`, `version = 1`, `chainId` de la chaîne et `verifyingContract` égal à l'adresse du DvP.

Type exact, ordre significatif :

```text
Trade(bytes32 tradeId,address buyer,address seller,address cashToken,address securityToken,uint256 cashAmount,uint256 securityAmount,uint256 deadline)
```

- `buyer` achète les titres et paie le cash ; `seller` livre les titres. BUY/SELL ne figure pas dans le message.
- `tradeId` est non nul. Le moteur devra générer un identifiant unique par trade accepté.
- Les tokens sont signés et doivent correspondre à la paire immutable du DvP.
- Les montants sont des entiers bruts à six décimales encodés en `uint256`. Rust reste actuellement borné à `u64` ; le contrat ne recalcule ni prix ni arrondi.
- `deadline` est un timestamp Unix en secondes. `block.timestamp >= deadline` est refusé. Le script de démo utilise cinq minutes ; les durées des futures RFQs restent à décider.

Les contreparties signent le même digest. `hashTrade(trade)` expose ce digest ; `eip712Domain()` expose le domaine. Signers EOA via ECDSA ; ERC-1271 reporté.

## Exécution et replay

`settle(trade, buyerSignature, sellerSignature)` accepte n'importe quel submitter. Les signatures fixent les destinataires et les montants. Le submitter paie le gas sans recevoir de frais contractuels.

Le contrat vérifie les termes, le temps et les signatures, marque le trade consommé, puis transfère cash et titres avec `SafeERC20`. `ReentrancyGuard` empêche une exécution réentrante. Un revert annule transferts et consommation. L'événement `TradeSettled` contient identifiant, contreparties et montants.

Le statut est `settled(buyer, seller, tradeId)`. Le scope aux contreparties évite que deux autres wallets consomment volontairement un identifiant public pour bloquer son usage légitime. Pour le même couple orienté, cet identifiant est inutilisable après settlement, même avec de nouveaux montants signés. Deux couples distincts peuvent utiliser le même identifiant on-chain ; le moteur devra néanmoins générer des identifiants globalement uniques dans sa session.

Aucune annulation anticipée des signatures. Elles restent potentiellement exécutables jusqu'à consommation ou deadline, même après un revert. Ne pas libérer une future réservation simplement parce qu'une tentative a échoué.

## Limites de confiance

Le DvP ne consulte pas Rust et ne prouve pas l'existence d'une RFQ acceptée. Le futur signer MM devra relier sa signature à une réservation valide. Les fonds restent dans les wallets ; les allowances ne garantissent pas les soldes futurs.

Paire fixée au déploiement, sans admin du DvP ni upgrade. Le constructeur vérifie deux adresses de contrats distinctes, pas leur comportement ERC-20. L'échange des quantités exactes suppose les tokens standards fournis, sans frais/rebase/hooks malveillants. Ce contrat ne prétend pas gérer des tokens arbitraires.

## Fixtures et dépendances

[buy-100.typed-data.json](../contracts/fixtures/buy-100.typed-data.json) contient le BUY 100 au meilleur ask Rust : 9 804,921500 sEUR, soit `9804921500` atomes contre `100000000` atomes tBILL. Les adresses sont fictives et fixes. Les [signatures](../contracts/fixtures/buy-100.signatures.json) sont générées par `cast wallet sign --data --from-file`, avec les clés publiques de test `0xA11CE` et `0xB0B` complétées sur 32 octets. Ne jamais financer ces comptes sur un réseau réel.

Le test Foundry charge la fixture et vérifie que le digest Solidity récupère les signers des signatures Cast : deux encodeurs distincts sont confrontés. Les futurs tests Go et wallet TypeScript reprendront cette fixture ; leur compatibilité n'est pas encore validée.

Dépendances dans `contracts/lib`, ignoré par Git et réinstallable avec `contracts/install-deps.ps1` :

- OpenZeppelin Contracts v5.4.0 : `c64a1edb67b6e3f4a15cca8909c9482ad33a02b0`.
- forge-std v1.9.7 : `77041d2ce690e692d6e03cc812b57d1ddaa4d505`.

Commits complets épinglés, aucun sous-module ni commit automatique. OpenZeppelin fournit les primitives de [signature EIP-712](https://github.com/OpenZeppelin/openzeppelin-contracts/blob/v5.4.0/contracts/utils/cryptography/EIP712.sol) et de [transfert ERC-20](https://github.com/OpenZeppelin/openzeppelin-contracts/blob/v5.4.0/contracts/token/ERC20/utils/SafeERC20.sol).

## Validation

17 tests Foundry réussis, dont deux tests fuzz à 256 cas chacun : BUY/SELL, montants et supply, mint, signatures altérées, changement de chaîne/contrat, replay, scope des identifiants, deadline, mauvais tokens et rollback. La réutilisation des signatures après correction d'une allowance est testée explicitement.

`scripts/test-settlement.ps1` a déployé et financé Anvil vierge sur 18545, broadcasté BUY 100 avec MM2 et SELL 100 avec MM3, puis vérifié soldes, supplies et statuts consommés. Le trader termine avec 1 000 tBILL et 99 996 sEUR : l'aller-retour coûte 4 sEUR de spread. L'instance temporaire est arrêtée en fin de test.

Build et `forge fmt --check` réussis. Foundry nightly a émis des warnings de lecture des sources pour les traces et de configuration Etherscan absente pendant les scripts ; broadcasts et lectures on-chain ont réussi. Aucun audit externe effectué.

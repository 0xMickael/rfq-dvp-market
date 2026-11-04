# Référence API et signatures

Référence technique ; commencer par [architecture.md](architecture.md) pour le fonctionnement et [development.md](development.md) pour lancer la démo.

## Conventions

Go écoute sur `127.0.0.1:8080`, Rust sur `127.0.0.1:8081`. Vite relaie `/api` vers Go. Requêtes JSON avec `Content-Type: application/json`, corps limités à 4 Kio, réponses Rust limitées à 64 Kio côté client Go. Les montants JSON sont des chaînes décimales, jamais des nombres flottants. `side` vaut `BUY`/`SELL`, `unit` vaut `tBILL`/`sEUR` ; saisie positive avec six décimales maximum.

Go attend Rust au maximum deux secondes ; le frontend abandonne ses requêtes après cinq secondes. Cela ne prouve pas l'annulation d'une opération déjà exécutée. Les réponses métier Go portent `Cache-Control: no-store`. `/healthz` indique seulement la liveness du processus.

## API Go

- `GET /api/config` : `signing_enabled`, `settlement_enabled` et `deployment` public lorsque configuré. Sans signer, seul `signing_enabled: false` est fourni. Aucune clé privée.
- `POST /api/quotes` : cotation indicative sans wallet, ni RFQ stockée, identifiant permettant une acceptation ou expiration.
- `POST /api/rfqs` : création de RFQ avec wallet EVM non nul distinct des MM ; adresse normalisée. La création ne prouve pas encore son contrôle.
- `GET /api/rfqs/{id}` : RFQ et trade éventuel conservés par Rust.
- `GET /api/rfqs/{id}/quotes/{quote}/terms` : `typed_data`, quote et expiration ; refus si RFQ expirée ou déjà acceptée.
- `POST /api/rfqs/{id}/accept` : signature trader vérifiée, réservation puis signature MM et mise en suivi si RPC activé.
- `GET /api/settlements/{trade_id}` : statut, message, hash éventuel ; 404 `UNKNOWN_SETTLEMENT` si travail absent de cette session Go.
- `GET /api/balances/{account}` : `sEUR` et `tBILL` en chaînes à six décimales ; exige le module RPC.

Cotation indicative :

```json
{"side":"BUY","unit":"tBILL","amount":"100"}
```

Pour créer une RFQ, ajouter `trader_id` avec l'adresse du wallet. Succès 201 : `rfq_id`, `trader_id`, `status`, `created_at`, `quote_expires_at`, `settlement_deadline`, `quotes`, `rejections`, `trade`. Les timestamps sont des secondes Unix. La démo fixe les échéances à création + 120/+ 600 s, sans choix client.

Chaque quote identifiée contient `quote_id`, `trade_id`, `maker_id`, `side`, `unit_price`, `quantity_tbill`, `cash_amount`. La première est la meilleure ; n'importe quelle quote exécutable de cette RFQ peut être choisie. Sans offre possible : 422, sans RFQ créée. Les refus par MM indiquent `maker_id`, `code`, `message`.

Acceptation : envoyer seulement `quote_id`, `trade_id` et `signature`, avec les IDs reçus et la signature EIP-712. Aucun montant ou domaine client n'est accepté. Le premier succès 200 contient `status: SIGNED`, `trade`, `typed_data`, `buyer_signature`, `seller_signature`, `settlement_submitted: false` et, si RPC activé, un objet `settlement`. Le worker peut soumettre ensuite : ce booléen initial n'est pas le suivi du trade.

Un retry valide d'un travail déjà connu retourne son `status` et son objet `settlement`, sans nouvelle signature ni réservation, même après deadline. Une course initiale peut recevoir `ALREADY_ACCEPTED` avant qu'un retry retrouve le résultat. Le mode sans RPC peut reconstituer les signatures d'une réservation valide avant la deadline.

Le suivi vaut `SETTLEMENT_PENDING`, `SETTLED` ou `SETTLEMENT_FAILED`. Un hash peut appartenir à une transaction signée dont l'envoi reste incertain : ne pas l'interpréter comme un receipt réussi.

## API interne Rust

- `POST /quotes` : même cotation indicative ; tient compte des réservations courantes.
- `POST /rfqs` : mêmes champs métier ; `trader_id` déclaratif de 1–64 caractères ASCII alphanumériques, `_` ou `-`, sans authentification.
- `GET /rfqs/{rfq_id}` : `QUOTED`, `EXPIRED` ou `ACCEPTED`, avec trade éventuel. Expiration évaluée à la lecture/acceptation.
- `POST /rfqs/{rfq_id}/accept` : seulement `trader_id`, `quote_id`, `trade_id`. Succès `RESERVED`, sans signature ni transfert. Une RFQ produit au plus un trade.
- `POST /rfqs/{rfq_id}/settlement` : `trade_id`, `status`, `transaction_hash` nullable, `block_timestamp`. Appel réservé au Go de confiance, sans montant fourni. La chaîne n'est pas vérifiée par Rust.

Le trade conserve `trade_id`, `rfq_id`, `trader_id`, `quote`, `accepted_at`, les deux échéances, `reserved_asset`, `reserved_amount`, `status` et `transaction_hash`. `settlement_authorized: false` rappelle que cette réponse interne ne constitue aucune signature ou autorisation on-chain. Les champs de réservation conservent les termes historiques même après résolution ; l'inventaire disponible est mis à jour séparément.

Les RFQ/quote IDs sont des compteurs locaux. Le trade ID est fixé dès la quote : nonce OS aléatoire de session 128 bits et compteur 128 bits, encodés en 32 octets hexadécimaux non nuls. Cela réduit le risque de réutiliser un ancien ID après restart, sans restaurer l'état perdu.

## Refus et erreurs

Erreurs métier : `code`, `message`, `rejections`. Codes principaux :

- 400 : `INVALID_REQUEST`, `INVALID_AMOUNT`, `INVALID_TRADER`, `INVALID_ADDRESS`.
- 403 : `INVALID_SIGNATURE` dans Go ; `WRONG_TRADER` dans Rust (égalité déclarative, pas authentification).
- 404 : `UNKNOWN_RFQ`, `UNKNOWN_QUOTE`, `UNKNOWN_SETTLEMENT`.
- 409 : `WRONG_TRADE_ID`, `ALREADY_ACCEPTED`, `INSUFFICIENT_INVENTORY`, `RESERVATION_MISMATCH`, `INVALID_TRANSITION`.
- 410 : `QUOTE_EXPIRED`, `QUOTE_UNAVAILABLE`, `SETTLEMENT_DEADLINE`.
- 422 : `NO_EXECUTABLE_QUOTE` ; Rust utilise aussi 422 pour certaines désérialisations invalides.
- 500 : `SIGNING_FAILED`, `SETTLEMENT_QUEUE_FAILED` ; une réservation peut déjà exister.
- 502 : `INVALID_ENGINE_RESPONSE` ; 503 : `ENGINE_UNAVAILABLE`, `SIGNING_DISABLED`, `SETTLEMENT_DISABLED`, `RPC_UNAVAILABLE`.

Mauvais Content-Type : 415 ; corps trop grand : 413 dans Rust. Go normalise les erreurs de décodage du corps en 400. Refus individuels MM : `INSUFFICIENT_INVENTORY`, `BELOW_MINIMUM_AMOUNT`, `AMOUNT_OUT_OF_RANGE`.

Une erreur réseau n'est pas une preuve de non-réservation. Le frontend conserve en mémoire la signature d'une acceptation incertaine pour un retry ; un rechargement peut la perdre. Après mise en suivi, seul l'ID du dernier settlement est gardé dans `sessionStorage`.

## Contrat DvP et EIP-712

Schéma commun Solidity, Go et TypeScript :

```text
Trade(bytes32 tradeId,address buyer,address seller,address cashToken,address securityToken,uint256 cashAmount,uint256 securityAmount,uint256 deadline)
```

Domaine : `name = rfq-dvp-market`, `version = 1`, `chainId` de la chaîne, `verifyingContract` = adresse du DvP. Pour BUY, trader = buyer et MM = seller ; pour SELL, rôles inversés. BUY/SELL n'est pas un champ signé. Les montants `uint256` sont les entiers bruts à six décimales, actuellement bornés à `u64` par Rust.

Les deux contreparties signent le même digest. Go refuse les signatures EOA malformées, high-s, du mauvais compte ou de termes altérés ; `v` est normalisé à 27/28 pour OpenZeppelin. Pas de support ERC-1271.

`settle(trade, buyerSignature, sellerSignature)` accepte tout submitter. Le contrat vérifie la paire immutable, les signatures, `block.timestamp < deadline` et l'absence de replay. Il marque le trade consommé puis effectue les transferts avec `SafeERC20`, sous `ReentrancyGuard`. Un revert annule transferts et consommation. Pas d'annulation anticipée des signatures ni de frais contractuels.

`settled(buyer, seller, tradeId)` scope le replay au couple orienté : un autre couple ne peut pas bloquer l'ID légitime. Le même couple ne peut pas réutiliser l'ID après règlement, même avec d'autres montants. `hashTrade` expose le digest, `eip712Domain()` le domaine, `TradeSettled` l'événement avec ID, contreparties et montants. Les tokens SEUR/TBILL utilisent `Ownable` pour le mint ; le DvP n'a ni admin ni upgrade.

La fixture [buy-100.typed-data.json](../contracts/fixtures/buy-100.typed-data.json) et ses [signatures Cast](../contracts/fixtures/buy-100.signatures.json) fixent un BUY 100 pour 9 804,921500 sEUR : `100000000` atomes de tBILL contre `9804921500` atomes de sEUR. Adresses et clés fictives de test ; les tests Go, TypeScript et Foundry confrontent leurs encodeurs. L'ABI utilisée par Go est [dvp.json](../backend/internal/settlement/dvp.json).

Les primitives proviennent d'OpenZeppelin et de go-ethereum/ethers, pas d'une cryptographie maison. Les versions sont fixées dans [install-deps.ps1](../contracts/install-deps.ps1), [go.mod](../backend/go.mod) et [package.json](../web/package.json). Voir le contrat [DvPSettlement.sol](../contracts/src/DvPSettlement.sol) pour la définition faisant autorité.

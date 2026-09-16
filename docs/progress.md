# Reprise du projet

## Jalon 1 — domaine validé le 16 septembre 2026

Le skeleton initial est dans le commit `274fbf1` (`chore: bootstrap rfq-dvp-market monorepo`). Le jalon suivant ajoute le domaine de cotation Rust. La première vertical slice complète n'est pas encore réalisée.

### Décisions à conserver

- BUY et SELL avancent ensemble ; 100 tBILL est seulement un exemple.
- Saisie au choix : quantité exacte de tBILL ou montant de sEUR, jusqu'à six décimales.
- Convention cash : budget maximal pour BUY, minimum à recevoir pour SELL.
- Refus explicite si un MM ne peut pas servir toute la quote ; aucune réduction silencieuse, agrégation de MM ou partial fill.
- Rust calcule les montants et décide du meilleur prix. Go orchestrera sans reproduire le pricing.

Les formules, arrondis et paramètres de démonstration sont détaillés dans [market-model.md](market-model.md).

### Code disponible

- [amount.rs](../market-engine/src/domain/amount.rs) : entiers à six décimales, parsing strict, bornes et affichage.
- [pricing.rs](../market-engine/src/domain/pricing.rs) : prix de référence, skew d'inventaire, bid/ask et disponibilité sur snapshot.
- [quote.rs](../market-engine/src/domain/quote.rs) : demandes BUY/SELL, conversions cash/tBILL, refus par MM, classement et trois configurations de démonstration.
- [mod.rs](../market-engine/src/domain/mod.rs) : types exportés et erreurs explicites.
- [tests/quotes.rs](../market-engine/tests/quotes.rs) : comportements et limites numériques.
- [examples/quotes.rs](../market-engine/examples/quotes.rs) : démonstration exécutable sans réseau.

Le domaine est indépendant d'Axum, sans nouvelle dépendance. `Market::quote(&self, ...)` ne modifie pas le snapshot. Le champ `reserved` représente uniquement des réservations fournies en entrée ; aucun mécanisme d'acceptation ou de réservation n'est implémenté.

### Vérifications réussies

Sous Rust/Cargo 1.98.1 et MSVC, depuis `market-engine` :

```powershell
cargo test --locked --all-targets
cargo clippy --locked --all-targets -- -D warnings
cargo fmt --check
cargo build --locked
cargo run --locked --example quotes
```

Résultat : 22 tests métier et 2 tests HTTP réussis, aucun warning Clippy, format et build valides, exemple terminé avec succès. Les tests couvrent les quatre modes de saisie, les fractions, les refus, les réservations déjà présentes dans le snapshot, le déterminisme, les arrondis, les configurations invalides et les overflows. Une grille de 144 cas vérifie les bornes de conversion du mode cash.

Dans la démo, P_ref vaut 98,039215. MM2 offre le meilleur ask (98,049215), MM3 le meilleur bid (98,009215). Avec 1 000 sEUR saisis : BUY cote 10,198959 tBILL pour 999,999924 sEUR ; SELL cote 10,203123 tBILL pour 1 000,000075 sEUR.

Un répertoire de build séparé `.cache/stage1-rust` a permis de laisser les serveurs de l'utilisateur ouverts. Le [guide de développement](development.md) explique comment reproduire ce choix. Go, le frontend et Foundry sont inchangés et n'ont pas été retestés pour ce jalon.

### Limites à la fin du jalon 1

Les serveurs exposent toujours uniquement `/healthz`. Le frontend reste statique. Aucun identifiant RFQ/quote, stockage de RFQ, expiry, acceptation, verrou de réservation, wallet, signature EIP-712, contrat ou settlement n'est implémenté.

La cotation vérifie seulement la capacité du MM dans un snapshot. Elle ne garantit ni la capacité au moment d'une future acceptation, ni les soldes/allowances on-chain du trader ou du MM. Les tests actuels ne prouvent pas la prévention des doubles acceptations ou du double settlement.

## Jalon 2 — cotation réseau validée le 16 septembre 2026

Le formulaire appelle maintenant Go, qui transmet la demande au domaine Rust. BUY/SELL et les deux unités de saisie sont disponibles. Les quotes sont indicatives, sans réservation ou transaction.

Nouveaux points d'entrée :

- `market-engine/src/httpapi.rs` : DTO JSON, conversion vers le domaine et réponses de cotation.
- `backend/internal/engine/client.go` : client HTTP avec timeout de deux secondes et propagation du contexte, sans pricing Go.
- `backend/internal/httpapi/handler.go` : validation de forme et route `/api/quotes`.
- `web/src/main.ts` et `web/index.html` : formulaire, résultats, refus et invalidation des résultats lors d'un changement de saisie.
- `web/vite.config.ts` : proxy local `/api` vers Go, pour dev et preview.
- [quote-api.md](quote-api.md) : format des messages, statuts et limites.

Validation réussie : 26 tests Rust (22 domaine, 4 HTTP), Clippy sans warning, build Rust ; tests Go (5 fonctions de test avec sous-scénarios), go vet et build ; TypeScript strict et build Vite. `scripts/test-quotes.ps1` a validé le trajet réel Vite → Go → Rust sur les quatre modes, les refus par MM, le refus global, la précision invalide et le moteur arrêté. Tous les processus lancés par ce script ont été arrêtés.

Le navigateur pilotable n'a pas démarré (runtime Node de l'outil introuvable). L'interaction visuelle du formulaire reste à vérifier manuellement ; le build et les appels HTTP réels sont validés. Les contrats Solidity restent absents : aucun test de DvP n'est annoncé.

## Jalon 3 — contrats validés le 17 septembre 2026

- SEUR/TBILL : ERC-20 à six décimales, mint réservé à leur issuer.
- DvPSettlement : paire immutable, signatures des deux contreparties, deadline exclusive, transferts atomiques, événement et statut de settlement.
- Replay consommé par `(buyer, seller, tradeId)` pour éviter qu'un autre couple bloque un identifiant public. Le futur moteur conserve des identifiants globalement uniques.
- Fixture EIP-712 JSON et signatures Cast validées dans Foundry ; les signers Go/TypeScript restent à construire.
- Déploiement/financement Anvil et BUY/SELL reproductibles avec les scripts Foundry. Détail du protocole dans [settlement.md](settlement.md).
- Formulaire clarifié : « Acheter/Vendre des tBILL », quantité ou budget maximal/cash minimum ; résultats formulés en actifs payés et reçus.

Validation : 17 tests Foundry réussis, dont deux fuzz à 256 cas chacun ; format/build Foundry et build TypeScript/Vite réussis. `scripts/test-settlement.ps1` a broadcasté les déploiements et les deux trades sur Anvil vierge (port 18545), puis vérifié soldes, supplies et statuts. L'instance temporaire a été arrêtée ; les adresses JSON générées ne désignent donc pas une chaîne encore vivante. Les warnings de traces de Foundry nightly n'ont pas empêché ces validations.

Le frontend précédent a été confirmé fonctionnel par l'utilisateur. Les nouveaux libellés sont compilés ; leur inspection visuelle reste manuelle. Go/Rust n'ont pas changé à ce jalon et leurs tests n'ont pas été relancés.

## Prochaine étape : RFQs et réservation Rust

1. Ajouter identité trader, RFQ/quote/trade IDs, expiry commerciale et deadline de settlement distinctes. Les opérations doivent conserver les montants exacts de la quote.
2. Implémenter l'acceptation et la réservation dans une même section critique Rust, pour BUY et SELL. Tester deux demandes concurrentes de 100 face à 150 disponibles, double acceptation et frontière d'expiration.
3. Relier le wallet et la signature trader ; vérifier la fixture EIP-712 dans Go/TypeScript. Le signer MM ne signe qu'après réservation valide.
4. Ajouter submitter Go, receipts et notification idempotente de Rust. Ne pas libérer une réservation sur simple timeout ou revert : les signatures peuvent rester exécutables.
5. Relier le frontend jusqu'à SETTLED, puis valider le flow complet.

Les scripts Foundry ne remplacent pas le futur settlement Go. Aucun RFQ lifecycle, wallet connecté ou échange depuis le formulaire n'est encore livré. Ne pas ajouter PostgreSQL, queues durables, loadgen, dashboard ou reprise automatique. Actualiser ce document après chaque jalon validé.
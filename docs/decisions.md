# Décisions initiales

## Statut et portée

Ces décisions traduisent le plan approuvé par l'auteur et précisent `AGENTS.md`. La cotation réseau et les contrats DvP sont livrés ; l'acceptation et l'intégration du settlement restent à construire. Le [document de reprise](progress.md) distingue les validations réalisées de la cible.

Le nom canonique du projet est `rfq-dvp-market`, dans les documents, les instructions d'agents, les packages et l'interface. La crate Rust s'importe sous la forme `rfq_dvp_market_engine`, conformément aux identifiants Rust.

## 1. Deux processus applicatifs et HTTP/JSON

Go orchestre ; Rust décide du marché. HTTP/JSON local évite la compilation croisée FFI et le codegen gRPC. Le coût accepté est une frontière réseau avec timeouts et résultats parfois incertains, à traiter explicitement.

Les trois MM sont des configurations/stratégies dans Rust. Les signers MM simulés seront des modules Go ; aucun service MM supplémentaire. Le transport sera séparé du domaine pour permettre une évolution sans réécrire les règles métier.

## 2. Concurrence correcte dès la slice

Le durcissement de phase 2 ne reporte pas la prévention de l'overselling. Accepter, créer le trade et réserver sont une seule transition sous verrou Rust. La performance d'un verrou global sera mesurée avant toute optimisation.

Les quotes ne réservent pas : une quote non expirée peut devenir inexécutable faute d'inventaire. Ce compromis limite les réservations spéculatives, mais réduit la garantie commerciale d'une quote affichée.

## 3. EIP-712 anticipé

Décision explicitement confirmée par l'auteur : utiliser les signatures dès la première slice, malgré la mention « later » du cadrage. Sans elles ou une autorisation on-chain équivalente, les allowances seules ne protègent pas les termes acceptés contre une modification par le submitter.

Le trader et le MM signent les mêmes termes ; le contrat applique une deadline et une consommation unique du trade. Signer MM et submitter restent dans Go pour la démo, ce qui n'offre aucune isolation contre une compromission de ce processus.

## 4. État en mémoire, session Anvil jetable

Aucune base ni queue durable au départ. Les invariants de concurrence concernent une session vivante ; les redémarrages avec perte d'état imposent une réinitialisation coordonnée de la chaîne et des services.

La phase 2 devra définir la persistance des transitions Rust, la reprise du suivi Ethereum et leur cohérence. Un simple ajout de PostgreSQL côté Go ne résout pas cette question.

## 5. Lifecycle et temps

Séparer RFQ, trade et tentative Ethereum. Séparer expiry commerciale et deadline de settlement. Un timeout ou un revert ne permet pas nécessairement de libérer une réservation : des signatures peuvent rester exécutables. La politique détaillée figure dans le [lifecycle](trade-lifecycle.md).

## 6. Tooling natif minimal

Décision confirmée : Windows natif. Go utilise la bibliothèque standard ; Rust utilise Cargo/Axum/Tokio/Serde ; Solidity utilise Foundry ; le frontend utilise Vite/TypeScript/npm sans framework UI.

Les versions directes sont épinglées, sans obligation d'être les toutes dernières. Les lockfiles doivent être générés par leurs outils et conservés après résolution des dépendances ; ne jamais inventer leurs checksums. Aucune toolchain globale ne doit être installée automatiquement.

La toolchain de développement Rust est alignée sur `1.98.1`, version installée et explicitement approuvée par l'auteur. Le minimum de langage du package reste `1.94` ; cela ne constitue pas une validation de toutes les versions intermédiaires.

## 7. Montants et cotation

Choix confirmés par l'auteur : BUY et SELL ensemble ; saisie au choix en tBILL ou en sEUR ; fractions à six décimales ; refus explicite si la demande entière est impossible. BUY 100 est un exemple, pas une limite.

Conventions d'implémentation : le cash saisi est un budget maximal pour BUY et un minimum à recevoir pour SELL. Le [modèle de marché](market-model.md) fixe les arrondis, bornes, paramètres MM et le tie-break. Le domaine calcule sur un snapshot sans le modifier ; les réservations atomiques restent un jalon distinct. Aucune dépendance supplémentaire n'a été ajoutée.

## 8. Questions à finaliser avec la slice métier

- Durées de validité compatibles avec les confirmations wallet.
- DTO/routes d'acceptation et intégration Go/TypeScript de la fixture EIP-712. Le message signé Solidity/Cast est fixé dans [settlement.md](settlement.md).

Ces choix devront être résolus avant de coder les mécanismes concernés ; ils ne doivent pas être implicitement décidés dans quatre langages séparément.

## Reporté

PostgreSQL, auth, recovery, loadgen, métriques et dashboard relèvent de la phase 2 ; Sepolia, CI/CD et hébergement de la phase 3. Aucun order book, réseau de solvers, bridge, ZK, account abstraction ou fonds ERC-4626/7540 n'est ajouté.

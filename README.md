# rfq-dvp-market

Infrastructure de marché pédagogique : négocier un titre fictif `tBILL` contre du cash fictif `sEUR` par RFQ, puis régler l'échange atomiquement sur Anvil.

## État actuel : skeleton

Le dépôt contient deux serveurs de santé indépendants (Go et Rust), une page TypeScript statique et la configuration Foundry. Il n'existe encore ni RFQ, ni pricing, ni wallet connecté, ni contrat Solidity, ni transaction de settlement.

`GET /healthz` indique seulement que le processus HTTP répond. Il ne vérifie aucune dépendance et ne signifie pas que le marché est disponible. Go et Rust ne communiquent pas encore entre eux.

## Monorepo

- `backend/` : API Go ; orchestration et settlement à venir.
- `market-engine/` : package Rust ; moteur de marché à venir, adaptateur HTTP minimal présent.
- `contracts/` : configuration Foundry et emplacements des futurs contrats, tests et scripts.
- `web/` : Vite et TypeScript strict, sans framework UI.
- `loadgen/` : intention documentée, réalisation en phase 2.
- `docs/` : architecture, décisions et chemin vers la première slice.

Chaque composant utilise son outil natif. Il n'y a pas d'orchestrateur de monorepo ni de dépendance à Docker, WSL ou Make.

## Démarrer et vérifier

Suivre le [guide Windows natif](docs/development.md) pour les prérequis, versions, commandes et limites de validation. La première installation des dépendances Rust et npm nécessite un accès réseau.

## Comprendre le projet

1. [Architecture et responsabilités](docs/architecture.md)
2. [Décisions et ambiguïtés du cadrage](docs/decisions.md)
3. [Modèle de marché](docs/market-model.md)
4. [Lifecycle](docs/trade-lifecycle.md)
5. [Modèle de confiance](docs/trust-model.md)
6. [Échecs et récupération](docs/failure-model.md)
7. [Première vertical slice](docs/vertical-slice.md)

`AGENTS.md` reste le cadrage canonique. Les décisions confirmées qui précisent ce cadrage sont consignées dans `docs/decisions.md`. BUY est le premier jalon ; SELL sera nécessaire pour achever la phase 1.

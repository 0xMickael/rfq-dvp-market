# rfq-dvp-market

Infrastructure de marché pédagogique : négocier un titre fictif `tBILL` contre du cash fictif `sEUR` par RFQ, puis régler l'échange atomiquement sur Anvil.

## État actuel : cotation frontend → Go → Rust

Le formulaire demande des quotes BUY et SELL à Go, qui appelle Rust. Le trader choisit « Acheter des tBILL » ou « Vendre des tBILL », puis une quantité, un budget maximal ou un cash minimum à recevoir. Les contrats ERC-20 et le DvP signé sont testés séparément sur Anvil. Le formulaire ne réserve ni ne transfère encore d'actif : RFQs stockées, wallets et intégration du settlement restent à construire.

Pour reprendre le travail, commencer par le [jalon validé et la prochaine étape](docs/progress.md).

`GET /healthz` indique seulement que le processus HTTP répond. Il ne vérifie aucune dépendance et ne signifie pas que le marché est disponible. Le [contrat HTTP de cotation](docs/quote-api.md) décrit les messages et les erreurs.

## Monorepo

- `backend/` : API Go ; orchestration et settlement à venir.
- `market-engine/` : domaine de cotation Rust indépendant du réseau et adaptateur HTTP.
- `contracts/` : ERC-20, DvP EIP-712, tests et scripts Anvil ; voir [settlement.md](docs/settlement.md).
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

`AGENTS.md` reste le cadrage canonique. Les décisions confirmées qui précisent ce cadrage sont consignées dans `docs/decisions.md`. BUY et SELL avancent ensemble, avec des montants variables ; 100 tBILL est un exemple de test.

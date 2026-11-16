# État du projet

Mis à jour le 16 novembre 2026. Ce document décrit la version actuelle ; les anciens récits par jalon ont été condensés pour ne pas entretenir plusieurs états contradictoires.

## Fonctionnel

Le parcours local est complet : wallet → RFQ → quotes Rust → signature trader → réservation atomique → signature MM → transaction Go → DvP Anvil → résultat et soldes. BUY et SELL fonctionnent en tBILL ou sEUR, avec montants exacts à six décimales. L'auteur a confirmé le fonctionnement dans son navigateur après le raccord du settlement ; ce retour manuel ne constitue pas un test automatisé de tous les cas MetaMask.

Le moteur protège les inventaires contre la concurrence et les doubles acceptations. Le contrat protège les termes signés, le replay et l'atomicité. Le worker suit le résultat, conserve les transactions à réessayer et notifie Rust de manière idempotente. Pas de libération sur simple timeout ou revert.

## Validations conservées

Vérifications exécutées lors des jalons des 16–17 septembre 2026, pas relancées pour la réorganisation documentaire du 21 septembre :

- Rust : **39 tests**, formatage, Clippy sans warning et build. Montants/arrondis, quatre modes, deux demandes concurrentes de 100 face à 150, double acceptation, frontière exacte d'expiration, bon actif réservé, comptabilité et notifications idempotentes.
- Go : tests, `go vet` et build. Signatures et termes altérés, réservation préalable, erreurs réseau et cinq tests du submitter : pertes de réponse avant/après inclusion, retry de notification, revert, mauvais montants d'événement et nonce signé expiré.
- Frontend : **9 tests**, TypeScript strict et build Vite. Fixture EIP-712 commune, SELL/grands montants, validation des termes et gestion du wallet avec provider simulé.
- Solidity : **17 tests Foundry**, dont deux fuzz à 256 cas, format/build. Signatures, deadline, replay, permissions de mint et rollback. Contrats inchangés lors du dernier raccord Go.
- Intégration cotation et signatures : vrais processus Vite → Go → Rust, quatre modes et cas de refus ; signatures validées avec wallet logiciel.
- Intégration settlement : `scripts/test-app-settlement.ps1`, vrais BUY/SELL Go sur Anvil temporaire, quatre modes fractionnaires, receipts, soldes exacts trader/MM, retries, nonces concurrents, allowance absente puis release après deadline. Ce test ne pilote pas le DOM/MetaMask.

Le test Foundry séparé a aussi validé un aller-retour BUY/SELL 100 avec 4 sEUR de spread et les supplies attendues. Les warnings de traces Foundry nightly n'ont pas bloqué les broadcasts. Aucun audit externe ni benchmark de charge n'est revendiqué.

Repères historiques : skeleton `274fbf1`, checkpoint cotation/contrats `65855d4d8fda7b3339d5476d32de8b74b6538b4d`, puis RFQs/réservations, signatures/wallet et submitter applicatif. Les changements suivants sont encore non committés au moment de cette réorganisation. Aucun commit ni push effectué dans ce travail documentaire.

## Limites actuelles

- État Go/Rust en RAM ; pas de reprise après crash. Reset coordonné avec Anvil obligatoire après perte d'état.
- Inventaires de démonstration et comptes publics Anvil ; pas de suivi des transferts externes, pas d'authentification des API internes ni d'isolation des clés MM.
- Un worker, aucun remplacement de transaction ni hausse automatique du gas price ; recherches d'événements depuis le bloc 0, sans purge globale des travaux/RFQs.
- Pas de finality publique, gestion des reorgs après validation, ERC-1271, partial fills, PostgreSQL, load generator, monitoring, dashboard ou déploiement Sepolia.

Les choix et le sens de la persistance sont expliqués dans [architecture.md](architecture.md). Le [guide de développement](development.md) contient les commandes de reproduction.

## Jalons validés et suite

| Jalon | État |
|---|---|
| 1 — Vérifications reproductibles et CI | Réalisé localement ; workflow GitHub préparé, non exécuté |
| 2 — Persistance et reprise | Plan validé, implémentation non commencée ; attente de validation de fin du jalon 1 |
| 3 — Loadgen et mesures | Non commencé |
| 4 — Sepolia | Non commencé ; toute transaction publique nécessite un accord explicite |

### Jalon 1 — validation du 16 novembre 2026

Commande complète exécutée avec succès : ./scripts/verify.ps1. Tests Rust (40), Go, Solidity (17 dont deux fuzz) et frontend (9), formatage Rust/Go/Solidity, Clippy, go vet et builds. Intégrations Signatures puis Settlement via Vite : BUY/SELL dans les quatre modes, concurrence, signatures, retries, soldes exacts, snapshot marché, notification répétée et conservation/libération de réservation en cas d'allowance absente.

Rapport local : .cache/verification/3f75b35d35d942eea94c534b05729ded/summary.json. Les artefacts ne sont pas versionnés. Les tests RPC perdus restent des tests avec RPC simulé ; aucune reprise durable après crash n'est revendiquée.

Dépendances mises à jour séparément des mécanismes métier : Vite 8.3.8, Rolldown 1.2.19, source-map-js 1.2.2 (via PostCSS). npm audit --json exécuté après mise à jour : aucune vulnérabilité connue signalée. Ce résultat n'est pas un audit du code applicatif.

Isolation : ports dédiés, manifeste par exécution et broadcasts/cache Foundry distincts. Le premier essai du nouveau runner a révélé un code de sortie Windows indisponible malgré le succès du client ; conservation explicite du handle corrigée, puis suite complète réussie. Aucun changement de pricing, de signature EIP-712 ou de logique de settlement.

Les versions requises sont centralisées dans scripts/toolchains.json. Le workflow utilise des actions épinglées par SHA, permissions contents: read, Windows 2022 et un job Go -race sur Ubuntu 24.04. Aucun push effectué ; GitHub Actions et Go -race Linux non exécutés. Les warnings Foundry sont expliqués dans architecture.md.

Pour reproduire depuis la racine :

```powershell
.\scripts\verify.ps1
# Pour essayer l'application :
.\scripts\demo.ps1
```

Le lancement borné du demo.ps1 et l'intégration avant modifications avaient été validés pendant l'inspection. MetaMask n'a pas été piloté manuellement dans ce jalon. Le build du frontend vérifié conserve le design existant.

### Décisions pour les prochains jalons

PostgreSQL natif sous Windows (installation système à autoriser), Rust seul auteur des données de marché et Go auteur des intentions et travaux de settlement ; signatures trader durables avant réservation, transaction signée durable avant envoi. Tests avec arrêts réels aux cinq frontières du plan.

Loadgen et métriques après la reprise durable, exclusivement sur chaîne locale pour la charge. Sur Sepolia, affichage de l'inclusion puis comptabilisation après finalized ; démarrage quotidien sans Anvil, redéploiement ni mint. Hébergement public et publication du dépôt restent séparés.

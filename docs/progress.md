# État du projet

Mis à jour le 21 septembre 2026. Ce document décrit la version actuelle ; les anciens récits par jalon ont été condensés pour ne pas entretenir plusieurs états contradictoires.

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

## Prochain objectif : portfolio GitHub

Publier une première version honnête et reproductible du prototype local. Une version publique du code n'exige pas un déploiement Internet ou Sepolia.

1. Documentation simplifiée et explication pédagogique : réalisées ici. Quatre documents au lieu de treize, avec un README d'entrée. Aucun code applicatif modifié dans cette étape.
2. Avant publication : relire le diff complet des jalons, vérifier le parcours depuis un checkout propre, choisir la licence et préparer une courte capture ou vidéo de BUY/SELL. Vérifier les fichiers destinés au dépôt public et distinguer clairement les clés Anvil de tout secret réel. Cette revue de publication n'est pas encore réalisée.
3. Ajouter une CI minimale pour les vérifications reproductibles, puis préparer le commit et publier lorsque la version est prête. Publication encore à réaliser ; aucun commit ni push effectué lors de cette étape.
4. Évolution pédagogique possible ensuite : provoquer des interruptions réelles, définir les données à conserver et implémenter une reprise durable. PostgreSQL est une option envisagée, pas une décision d'implémentation prise ici. Loadgen et mesures de performance peuvent suivre ; pas de priorité au dashboard.

La documentation a été fusionnée : décisions/pricing/lifecycle/confiance/échecs dans `architecture.md`, lancement et tests dans `development.md`, routes et settlement signé dans `api.md`, validations et suite ici. Les liens locaux et le diff sont vérifiés lors de cette réorganisation ; les validations logicielles ci-dessus restent datées de leur exécution.

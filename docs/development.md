# Lancer et tester

## Prérequis et première installation

Le parcours vérifié est Windows natif / PowerShell, sans Docker ni WSL. Chaque commande indique son dossier. Les versions ci-dessous décrivent l'environnement validé, pas les dernières versions disponibles.

- Go : cible `1.26.8`, langage minimum `1.26.0` dans `backend/go.mod`.
- Rust : `1.98.1` dans `rust-toolchain.toml`, avec rustfmt, Clippy et linker MSVC (Visual Studio Build Tools, workload C++ et Windows SDK). Langage minimum du package : `1.94`.
- Node : contrainte `^20.19.0 || >=22.12.0` dans `web/package.json`. Environnement testé : Node `24.22.1`, npm `11.19.1`.
- Foundry : Forge/Anvil `1.9.0`. Solidity `0.8.30`, EVM `cancun`.

Depuis la racine, une fois par nouveau checkout :

```powershell
Push-Location contracts
./install-deps.ps1
Pop-Location
Push-Location web
npm.cmd ci --ignore-scripts
Pop-Location
```

`npm ci` installe les bibliothèques du frontend, pas les tokens ni le wallet. Inutile de le répéter à chaque lancement. Le script Foundry installe OpenZeppelin 5.4.0 et forge-std 1.9.7 à des commits épinglés, sans sous-module. Go et Cargo téléchargent leurs dépendances au premier build ; conserver `go.sum`, `Cargo.lock` et `package-lock.json`. Forge peut télécharger solc. Aucune installation globale de toolchain n'est automatisée par le dépôt.

## Démo complète : un terminal

Depuis la racine du dépôt, avec les prérequis et dépendances installés :

```powershell
.\scripts\demo.ps1
```

Le script recharge le PATH enregistré pour retrouver les outils installés depuis l'ouverture de VS Code, sans modifier le PATH permanent. Il vérifie les dépendances et les ports 8545/8081/8080/5173, compile les composants, lance une chaîne Anvil vierge, déploie les contrats et démarre Rust, Go et le frontend. Il vérifie aussi une cotation via le proxy frontend avant d'afficher `DEMO PRETE`.

Ouvrir **http://127.0.0.1:5173**, puis suivre l'essai MetaMask ci-dessous. Le compte déjà importé reste utilisable. Fermer les anciens onglets de démo pour ne pas conserver le suivi d'un trade de la session précédente.

Garder le terminal ouvert ; **Ctrl+C** arrête les quatre processus lancés par le script, dans l'ordre inverse du démarrage. Un échec au démarrage ou la sortie d'un service provoque également leur arrêt. Le script refuse un port occupé ou une deuxième session et ne tue aucun service préexistant. Ne pas fermer brutalement le processus PowerShell : son bloc de nettoyage ne pourrait alors pas s'exécuter.

Les logs stdout/stderr de chaque service sont conservés dans `.cache/demo/<session>/`. Le fichier de déploiement antérieur est restauré à l'arrêt ; il ne représente pas une chaîne encore active. Chaque relancement réinitialise les soldes et inventaires, sans restaurer les trades précédents. Le script de déploiement, Go et le frontend imposent Anvil 31337 ; les contrats eux-mêmes ne sont pas limités à ce réseau.

Le frontend sert le build via Vite preview : les modifications de source demandent un relancement. Pour développer avec hot reload, utiliser le parcours manuel ci-dessous. Aucun outil global ni dépendance manquante n'est installé automatiquement.

Pour une vérification bornée du démarrage et du nettoyage, `./scripts/demo.ps1 -RunSeconds 2` arrête la session deux secondes après qu'elle est prête. Ce contrôle ne signe aucun trade et ne remplace pas le test de settlement.

Validation du lanceur le 15 novembre 2026 : démarrage complet avec cotation via le frontend, arrêt borné, refus d'un port occupé sans arrêt du listener existant, nettoyage après échec de déploiement, relancement et Ctrl+C avec vérification des quatre ports libérés. Le parcours MetaMask avait été confirmé manuellement par l'auteur avant l'ajout du lanceur ; il n'a pas été automatisé ici.

Si PowerShell refuse l'exécution du script local, cette invocation autorise uniquement le processus lancé, sans changer la politique permanente :

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo.ps1
```

## Alternative : quatre terminaux

Chaque terminal commence à la racine du dépôt. Garder les serveurs ouverts.

**Terminal 1 — chaîne locale vierge**

```powershell
anvil --host 127.0.0.1 --port 8545 --chain-id 31337 --hardfork cancun --block-time 1
```

Un bloc par seconde permet aux deadlines on-chain d'avancer même sans autre transaction. Ne pas charger d'ancien état ni utiliser un fork distant.

**Terminal 2 — déploiement, puis moteur Rust**

```powershell
Set-Location contracts
forge script script/DeployLocal.s.sol:DeployLocal --rpc-url http://127.0.0.1:8545 --broadcast --non-interactive
# Vérifier le succès du déploiement avant de continuer.
Set-Location ../market-engine
cargo run --locked
```

Le script finance les tokens et prépare les allowances trader/MM. Il écrit les adresses dans `contracts/deployments/31337.json`, lu par Go au démarrage.

**Terminal 3 — backend Go**

```powershell
Set-Location backend
go run ./cmd/rfq-dvp-market-api -deployment ../contracts/deployments/31337.json -rpc http://127.0.0.1:8545
```

**Terminal 4 — frontend**

```powershell
Set-Location web
npm run dev
```

Ouvrir `http://127.0.0.1:5173`. Go écoute sur 8080, Rust sur 8081 et Anvil sur 8545. Vite relaie `/api` vers Go. `GET /healthz` indique seulement que le serveur répond, pas que ses dépendances fonctionnent.

## Essai MetaMask

1. Importer le compte **index 1** du mnemonic public standard affiché par Anvil : adresse trader `0x70997970C51812dc3A010C7d01b50e0d17dc79C8`. La clé commençant par `0xac09` est celle de l'index 0, l'issuer, pas celle du trader.
2. Cliquer sur « Connecter le wallet ». Accepter le passage sur Anvil 31337, RPC `http://127.0.0.1:8545`.
3. Demander BUY ou SELL en tBILL ou sEUR, puis « Signer et exécuter » avant l'expiration de la quote (120 s).
4. Signer le message EIP-712. Go envoie la transaction et paie le gas depuis l'index 5. Attendre `SETTLED`, le hash et les soldes actualisés.

Indices de la démo : 0 issuer, 1 trader, 2–4 MM1–MM3, 5 submitter. Ce sont des comptes de test publiquement connus, exclusivement pour la chaîne locale.

Sans `-rpc`, Go propose les signatures et réservations sans transfert. Sans `-deployment`, seule la cotation indicative fonctionne. Il n'y a pas de `.env` lu automatiquement. Go accepte aussi `-listen` et `-engine` ; Rust accepte `ENGINE_ADDR` pour les tests sur d'autres ports.

## Arrêt et réinitialisation

Ctrl+C arrête chaque serveur. Vite recharge le frontend ; Go/Rust doivent être relancés après modification. Une perte d'état Go ou Rust exige un **reset coordonné** : arrêter les services et Anvil, recréer une chaîne vierge, redéployer et relancer. Ne pas conserver la chaîne avec un inventaire Rust remis à son état initial. Si l'interface suit un ancien trade, fermer son onglet puis en ouvrir un nouveau (suivi dans `sessionStorage`). Voir [pourquoi la persistance manque](architecture.md#persistance-et-reprise).

## Vérifications reproductibles

Commande recommandée depuis la racine :

```powershell
.\scripts\verify.ps1
```

Versions exactes : scripts/toolchains.json. Le script ne fait aucune installation globale. Il contrôle formatage, tests, analyses et builds puis exécute les intégrations Signatures et Settlement via Vite. L'option -SkipIntegration ne valide qu'une suite partielle.

Rapports : .cache/verification/<exécution>/summary.json et verify.log. Logs et manifestes isolés : .cache/integration/. Ports de test : 15173, 18080, 18081, 18545. Les tests refusent les ports occupés, arrêtent leurs seuls processus et ne remplacent pas le manifeste de démo.

Le workflow GitHub prépare Windows 2022 avec les mêmes versions et un job Ubuntu 24.04 limité au race detector Go. Actions épinglées par SHA, permissions en lecture seule, aucun secret applicatif. Les installations globales du workflow concernent uniquement le runner jetable. L'exécution GitHub reste à valider après push autorisé.

### Commandes individuelles

Exécuter chaque bloc depuis la racine et vérifier le succès des commandes avant de poursuivre.

```powershell
Push-Location market-engine
cargo fmt --check
cargo test --locked --all-targets --target-dir ../.cache/stage1-rust
cargo clippy --locked --all-targets --target-dir ../.cache/stage1-rust -- -D warnings
cargo build --locked --target-dir ../.cache/stage1-rust
Pop-Location

Push-Location backend
gofmt -l cmd internal
go test ./...
go vet ./...
go build -o ../.cache/settlement-api.exe ./cmd/rfq-dvp-market-api
Pop-Location

Push-Location contracts
forge fmt --check
forge test -vv
forge build
Pop-Location

Push-Location web
npm test
npm run build
Pop-Location

./scripts/test-app-settlement.ps1
```

`gofmt -l` ne doit rien afficher. Le répertoire Rust distinct évite de remplacer l'exécutable d'un serveur ouvert sous Windows. Le test applicatif utilise un wallet logiciel ethers, une chaîne temporaire 18545 et Go/Rust 18080/18081. Il teste les quatre modes, soldes exacts, receipts, retries, nonces et échec d'allowance. Il arrête ses processus et restaure le JSON de déploiement précédent ; il ne pilote pas MetaMask.

### Tests ciblés facultatifs

- Domaine sans serveur : depuis `market-engine`, `cargo run --locked --example quotes`.
- Contrats seuls : depuis la racine, `./scripts/test-settlement.ps1`. Port 18545 libre ; ce script écrit un JSON de déploiement pour une chaîne ensuite arrêtée. Redéployer avant une session interactive. Ne pas lancer `SettleLocal.s.sol` sur la session applicative pour modifier les inventaires hors Rust.
- Cotation via Vite : compiler Go en `.cache/quotes-api.exe` (`go build -o ../.cache/quotes-api.exe ./cmd/rfq-dvp-market-api` depuis `backend`), puis `./scripts/test-quotes.ps1` depuis la racine.
- Signatures seules : compiler Go en `.cache/signatures-api.exe` de la même manière, puis `./scripts/test-signatures.ps1`. Contrats fictifs, aucun transfert Anvil.

Les deux derniers scripts exigent le build Rust `.cache/stage1-rust`, le build web et les ports 8080/8081/5173 libres ; le test de signatures exige aussi `npm test` pour produire les modules TypeScript utilisés. Les scripts ne stoppent pas les services préexistants. Les résultats historiques sont dans [progress.md](progress.md).

## Erreurs courantes

- **Quote expirée** : demander une nouvelle RFQ ; reconnecter le wallet ne prolonge pas ses termes.
- **Signatures ou settlement désactivés** : vérifier les flags `-deployment` et `-rpc` de Go.
- **Contrat absent / mauvaise paire** : vérifier que le JSON correspond au déploiement de la chaîne actuellement ouverte.
- **Simulation refusée** : vérifier le compte trader index 1, ses soldes et allowances. Les réservations restent conservatrices jusqu'à résolution ; ne pas les effacer sur un simple timeout.
- **Port occupé** : arrêter le service concerné ou choisir les ports de test. Aucun changement silencieux de port.
- **Erreur linker Rust sous Windows** : vérifier les Build Tools C++ et le SDK, pas seulement rustup.

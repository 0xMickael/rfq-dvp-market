# Développement local — Windows natif

## État actuel et prérequis

Les services exposent leur santé et la cotation indicative. Les commandes ci-dessous ne déploient aucun contrat et ne transfèrent aucun actif. Ouvrir un terminal PowerShell à la racine du dépôt ; chaque section indique ensuite son répertoire.

Prérequis du projet : Go, Rust et les outils C++ de Visual Studio ont été vérifiés sur cette machine. Sur une autre machine, les toolchains sont à installer par le développeur, pas par un script automatique du dépôt :

- **Go** : toolchain cible `1.26.8`, minimum de langage `1.26.0` dans `backend/go.mod`. Le module `rfq-dvp-market.local/backend` est local, sans dépendance externe ni besoin de `go.sum`. [Installation officielle](https://go.dev/doc/install)
- **Rust** : toolchain `1.98.1`, avec Cargo, rustfmt et Clippy, sélectionnée par `rust-toolchain.toml`. Le minimum de langage déclaré dans `Cargo.toml` reste `1.94` ; la toolchain de développement est `1.98.1`. Sur Windows, utiliser le target natif MSVC et installer les Visual Studio Build Tools avec le workload « Développement Desktop en C++ » et son Windows SDK. Rustup seul ne fournit pas le linker MSVC. [Installation officielle](https://www.rust-lang.org/tools/install)
- **Node/npm** : Node `20.20.2` et npm `11.17.0` sont les versions observées dans cet environnement. Pour une nouvelle installation, préférer une branche Node LTS encore supportée, compatible avec le champ `engines` du frontend. TypeScript est épinglé à `5.9.3` et Vite à `8.3.0` dans le manifest et le lockfile.
- **Foundry** : Forge/Anvil observés `1.6.0-nightly`, commit `a0b89547df41c2ac5c28eb795ec1685927b461c1`. Cette référence décrit l'environnement utilisé, pas une exigence de mise à jour globale. Solidity est épinglé à `0.8.30` et la cible EVM à `cancun` dans `foundry.toml`. [Installation officielle](https://getfoundry.sh/introduction/installation/)

Après installation de Go/Rust, ouvrir un nouveau PowerShell pour recharger le PATH :

```powershell
go version
rustup --version
cargo --version
node --version
npm --version
forge --version
anvil --version
```

Le choix des versions directes est reproductible et ne signifie pas « dernière version disponible ». Les évolutions se font explicitement, avec vérification des builds et des lockfiles.

## Démarrage courant

Après la première installation des dépendances, ouvrir trois nouveaux terminaux PowerShell à la racine du dépôt, afin qu'ils héritent du PATH actualisé :

- **API Go** : `Set-Location backend`, puis `go run ./cmd/rfq-dvp-market-api`.
- **Moteur Rust** : `Set-Location market-engine`, puis `cargo run --locked`.
- **Frontend** : `Set-Location web`, puis `npm run dev`.

Ouvrir ensuite `http://127.0.0.1:5173`. Chaque serveur occupe son terminal ; utiliser Ctrl+C pour l'arrêter. Anvil est facultatif tant qu'aucun settlement n'est implémenté. Le formulaire appelle Go via le proxy Vite ; Go appelle Rust. Après une modification de Go/Rust, redémarrer leur commande ; Vite recharge le frontend automatiquement.

Il n'est pas nécessaire de réinstaller les outils ni de relancer `npm ci` à chaque démarrage. Les sections suivantes détaillent l'installation des dépendances et les vérifications, à exécuter après un nouveau checkout ou des modifications.

## Go

Depuis la racine :

```powershell
Set-Location backend
go build ./...
go vet ./...
go test ./...
gofmt -l cmd internal
go run ./cmd/rfq-dvp-market-api
```

`gofmt -l` doit ne rien afficher. Les tests couvrent le statut HTTP, le JSON de santé et le refus des routes/méthodes non supportées. Le serveur écoute `127.0.0.1:8080` ; Ctrl+C arrête le processus local. Il n'y a pas encore de worker à drainer.

La directive Go `toolchain` peut demander un téléchargement si la version installée est plus ancienne. Installer la version cible avant les vérifications pour maîtriser cette étape.

## Rust

Depuis la racine :

```powershell
Set-Location market-engine
cargo build --locked
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked --all-targets
cargo run --locked
```

`Cargo.lock` est généré et fait partie des fichiers du projet à conserver dans Git. Utiliser les commandes `--locked` sans le régénérer à chaque build. Les dépendances transitives ne sont pas figées par les seuls numéros exacts des dépendances directes.

Rustup sélectionne la toolchain déclarée et peut la télécharger à sa première utilisation. Cargo télécharge les crates lors de la première compilation. Le serveur écoute `127.0.0.1:8081` et s'arrête avec Ctrl+C. Les tests Axum appellent le router en mémoire sans ouvrir de port.

### Démonstration du domaine de cotation

Dans un quatrième terminal, depuis la racine :

```powershell
Set-Location market-engine
cargo run --locked --example quotes
```

Cette commande affiche les quotes des trois MM pour BUY/SELL en tBILL et en cash, puis se termine. Aucun serveur ni Anvil n'est requis ; aucun actif n'est réservé ou transféré. Le formulaire web permet les mêmes choix via les services.

Si un serveur Rust ouvert verrouille un exécutable lors d'un build Windows, utiliser un répertoire de compilation distinct dans ce terminal uniquement, avant les commandes Cargo :

```powershell
$env:CARGO_TARGET_DIR = Join-Path (Split-Path (Get-Location)) '.cache/stage1-rust'
```

Cette commande suppose que le terminal se trouve dans `market-engine`. Elle ne modifie aucune configuration globale ; le premier build dans ce répertoire recompile les dépendances.

## Frontend

Depuis la racine :

```powershell
Set-Location web
npm ci --ignore-scripts
npm run typecheck
npm run build
npm run dev
```

Ouvrir `http://127.0.0.1:5173`. Choisir BUY/SELL, tBILL/sEUR et un montant, puis demander les quotes. Le résultat affiche la meilleure offre et les refus. Changer un champ efface les résultats précédents. Aucune connexion wallet ni exécution n'est encore proposée.

`npm ci` utilise `package-lock.json`. Les scripts d'installation des dépendances sont désactivés ; les scripts du projet restent exécutables via `npm run`. Pour inspecter le bundle produit, arrêter le serveur de développement puis utiliser `npm run preview` sur le même port. Vite échoue si le port est occupé, au lieu d'en choisir un autre.

## Foundry et Anvil

Depuis la racine :

```powershell
Set-Location contracts
./install-deps.ps1
forge config --json
forge build
forge fmt --check
forge test -vv
```

Installer les dépendances une fois par checkout : OpenZeppelin 5.4.0 et forge-std 1.9.7 sont épinglés par commit complet dans le script. `contracts/lib` est ignoré par Git. Forge peut télécharger `solc` 0.8.30 au premier build. Les tests vérifient les contrats et la fixture EIP-712 ; voir [settlement.md](settlement.md).

Anvil n'est pas nécessaire pour la cotation. Pour les contrats, le démarrer dans un terminal dédié :

```powershell
anvil --host 127.0.0.1 --port 8545 --chain-id 31337 --hardfork cancun
```

Ne pas utiliser de fork distant ni charger d'état existant. Les scripts utilisent le mnemonic public standard Anvil : index 0 issuer, 1 trader, 2–4 MM1–MM3 et 5 submitter. Les soldes ETH sont fournis par Anvil ; le script finance les tokens et prépare les allowances.

Dans un autre terminal, depuis `contracts` :

```powershell
forge script script/DeployLocal.s.sol:DeployLocal --rpc-url http://127.0.0.1:8545 --broadcast --non-interactive
forge script script/SettleLocal.s.sol:SettleLocal --rpc-url http://127.0.0.1:8545 --broadcast --non-interactive
```

Vérifier le succès du déploiement avant le settlement. `deployments/31337.json` contient les adresses publiques ; une simulation peut aussi écrire ce fichier, donc vérifier les receipts. Le scénario utilise des identifiants fixes et ne se rejoue pas sans remettre Anvil à zéro. Ces scripts ne passent pas par Go/Rust.

Test intégré isolé, depuis la racine : `./scripts/test-settlement.ps1`. Le port 18545 doit être libre. Il démarre Anvil, déploie et exécute BUY/SELL, vérifie les soldes puis arrête sa propre instance. Le JSON généré correspond ensuite à une chaîne arrêtée : redéployer pour une session interactive.

## Vérifier les serveurs démarrés

Dans un autre terminal :

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8081/healthz
```

Chacun retourne `status: ok`. Les adresses sont des constantes locales du skeleton ; aucun `.env` n'est lu, aucun mécanisme de configuration caché n'est présent. Les ports pourront devenir configurables lorsque la slice en aura besoin.

## État de validation — 15 septembre 2026

- La configuration Foundry est lisible et `forge build` répond `Nothing to compile`. `forge fmt --check` ne trouve aucune source à formater.
- Go `1.26.8` : build, `go vet`, tests HTTP et contrôle `gofmt` réussis avec les nouveaux noms de module et de commande.
- Rust/Cargo `1.98.1` et linker MSVC : build, `cargo fmt --check`, Clippy sans warning et les deux tests HTTP réussis. `Cargo.lock` est généré par Cargo.
- Les deux exécutables ont été démarrés temporairement : `GET /healthz` retourne `status: ok` sur `8080` et `8081`. Les processus de test ont ensuite été arrêtés.
- Le lockfile npm est généré par npm et ses versions correspondent au manifest. `npm run build` réussit (contrôle TypeScript strict puis build Vite). Après mise à jour vers Vite `8.3.0`, npm rapporte zéro vulnérabilité connue à la date de vérification. Ce constat ne remplace pas un audit de sécurité.
- Le serveur Vite démarre sur le port prévu. La page HTML et le module TypeScript transformé sont accessibles en HTTP. Aucun navigateur pilotable n'est disponible dans cette session : l'inspection visuelle et l'exécution du module dans un navigateur restent à faire manuellement.
- La sandbox Windows peut bloquer npm (`EPERM`) et empêcher Forge de retrouver le profil utilisateur. Ces restrictions de la session d'agent sont distinctes d'un prérequis manquant ou d'une erreur du code ; les vérifications concernées nécessitent alors l'autorisation d'exécuter hors sandbox.

## Validation du domaine — 16 septembre 2026

Rust 1.98.1 : `cargo test --locked --all-targets` réussit avec 22 tests métier et 2 tests HTTP ; `cargo clippy --locked --all-targets -- -D warnings`, `cargo fmt --check`, `cargo build --locked` et `cargo run --locked --example quotes` réussissent. Le build a utilisé `.cache/stage1-rust` pour préserver le serveur déjà ouvert. Go, web et Foundry n'ont pas changé et n'ont pas été revalidés pour ce jalon.

Voir [progress.md](progress.md) pour le périmètre exact et la prochaine étape.

## Reporté

Installation automatisée des toolchains, scripts d'orchestration multi-processus, configuration des clés, déploiement, CI/CD et monitoring. Pour la politique de réinitialisation après perte d'état, voir le [modèle d'échec](failure-model.md).

## Validation de la cotation réseau — 16 septembre 2026

26 tests Rust et tests Go réussis ; Clippy, go vet et builds Rust/Go/TypeScript/Vite valides. Le [test d'intégration](quote-api.md) traverse réellement Vite → Go → Rust et vérifie les quatre modes, les refus et le moteur indisponible. Les processus temporaires ont été arrêtés. L'interaction visuelle reste à vérifier manuellement : le navigateur pilotable n'a pas pu démarrer dans cette session.

# Développement local — Windows natif

## État actuel et prérequis

Les services exposent uniquement leur santé. Les commandes ci-dessous ne déploient aucun contrat et ne lancent pas de trading. Ouvrir un terminal PowerShell à la racine du dépôt ; chaque section indique ensuite son répertoire.

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

Ouvrir ensuite `http://127.0.0.1:5173`. Chaque serveur occupe son terminal ; utiliser Ctrl+C pour l'arrêter. Anvil est facultatif tant que le dépôt ne contient que le skeleton. Les deux serveurs répondent indépendamment : la page ne les appelle pas encore.

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
cargo test --locked
cargo run --locked
```

`Cargo.lock` est généré et fait partie des fichiers du projet à conserver dans Git. Utiliser les commandes `--locked` sans le régénérer à chaque build. Les dépendances transitives ne sont pas figées par les seuls numéros exacts des dépendances directes.

Rustup sélectionne la toolchain déclarée et peut la télécharger à sa première utilisation. Cargo télécharge les crates lors de la première compilation. Le serveur écoute `127.0.0.1:8081` et s'arrête avec Ctrl+C. Les tests Axum appellent le router en mémoire sans ouvrir de port.

## Frontend

Depuis la racine :

```powershell
Set-Location web
npm ci --ignore-scripts
npm run typecheck
npm run build
npm run dev
```

Ouvrir `http://127.0.0.1:5173`. La page doit afficher « rfq-dvp-market — skeleton » et « Frontend TypeScript chargé ». Elle ne fait aucun appel aux services et ne propose aucune connexion wallet.

`npm ci` utilise `package-lock.json`. Les scripts d'installation des dépendances sont désactivés ; les scripts du projet restent exécutables via `npm run`. Pour inspecter le bundle produit, arrêter le serveur de développement puis utiliser `npm run preview` sur le même port. Vite échoue si le port est occupé, au lieu d'en choisir un autre.

## Foundry et Anvil

Depuis la racine :

```powershell
Set-Location contracts
forge config --json
forge build
forge fmt --check
```

Il n'y a aucune source Solidity : `Nothing to compile` est attendu et **ne valide pas le futur DvP**. Aucun test métier n'est présent. Forge pourra télécharger `solc` lors de l'arrivée des premiers contrats.

Anvil n'est pas nécessaire pour les health endpoints ou la page statique. Pour préparer la prochaine slice, le démarrer manuellement dans un terminal dédié :

```powershell
anvil --host 127.0.0.1 --port 8545 --chain-id 31337 --hardfork cancun
```

Ne pas utiliser de fork distant ni charger d'état existant pour une session remise à zéro. Aucun compte, clé ou secret réel n'est nécessaire. Les contrats, le mint initial et les approvals ne seront ajoutés qu'au prochain jalon.

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

## Reporté

Installation automatisée des toolchains, scripts d'orchestration multi-processus, configuration des clés, déploiement, CI/CD et monitoring. Pour la politique de réinitialisation après perte d'état, voir le [modèle d'échec](failure-model.md).

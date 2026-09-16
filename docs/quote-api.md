# API de cotation indicative

## Disponible

Frontend → `POST /api/quotes` dans Go → `POST /quotes` dans Rust. Le proxy Vite conserve une même origine navigateur, sans configuration CORS. Les deux services conservent `/healthz`, qui ne sonde aucune dépendance.

```json
{"side":"BUY","unit":"tBILL","amount":"100"}
```

`side` accepte BUY/SELL ; `unit` accepte tBILL/sEUR. `amount` est une chaîne décimale positive à six décimales maximum. Pour sEUR : budget maximal BUY, minimum à recevoir SELL. Aucun wallet n'est demandé pour ce calcul indicatif.

Succès HTTP 200 : `quotes` contient les offres classées du meilleur prix au moins bon ; `rejections` contient les refus des autres MM. Chaque quote contient `maker_id`, `side`, `unit_price`, `quantity_tbill`, `cash_amount`. Tous les montants sont des chaînes à six décimales. La première quote est la meilleure.

Un refus contient `maker_id`, `code`, `message`. Les codes MM sont `INSUFFICIENT_INVENTORY`, `BELOW_MINIMUM_AMOUNT`, `AMOUNT_OUT_OF_RANGE`.

Une erreur contient `code`, `message`, `rejections` :

- 400 : demande ou montant invalide (`INVALID_REQUEST`, `INVALID_AMOUNT`).
- 415 : Content-Type incorrect dans Go.
- 422 : aucun MM ne peut servir la demande (`NO_EXECUTABLE_QUOTE`).
- 503 : moteur inaccessible, timeout, réponse JSON invalide/trop grande ou statut moteur inattendu (`ENGINE_UNAVAILABLE`).

Rust rejette aussi les erreurs de désérialisation avec 422 et les corps trop grands avec 413. Go normalise les erreurs de forme de la requête externe en 400. Les corps entrants sont limités à 4 Kio ; Go limite la réponse Rust à 64 Kio. Go transmet les réponses métier Rust sans convertir les montants en nombres.

## Temps et état

Le client Go a un timeout de deux secondes et transmet le contexte de la requête pour propager son annulation. Aucun retry automatique. Le frontend abandonne après cinq secondes et invalide un résultat en cours si les champs changent.

Le snapshot Rust est partagé via `Arc<Market>` sans mutation, donc sans Mutex. Une requête de cotation ne réserve rien et ne crée aucun identifiant persistant. Les résultats sont indicatifs : pas d'expiry, de signature, d'acceptation ou de settlement à ce jalon. Le contrat des futures RFQs exécutables reste à définir.

## Vérification reproductible

Voir [development.md](development.md) et [le script d'intégration](../scripts/test-quotes.ps1). Ce test traverse le proxy Vite et les deux processus réels, puis arrête uniquement ses propres processus. Les tests Go couvrent aussi timeout, réponses invalides, erreur moteur et conservation exacte des montants.

### Lancer le test d'intégration sous PowerShell

Depuis la racine, avec Go/Cargo/Node dans le PATH :

```powershell
$env:CARGO_TARGET_DIR = Join-Path (Get-Location) '.cache/stage1-rust'
cargo build --locked --manifest-path market-engine/Cargo.toml
Push-Location backend
go build -o ../.cache/quotes-api.exe ./cmd/rfq-dvp-market-api
Pop-Location
Push-Location web
npm run build
Pop-Location
./scripts/test-quotes.ps1
```

Vérifier le succès de chaque build avant de poursuivre. Les ports 8080, 8081 et 5173 doivent être libres : le script refuse de toucher à des services existants. Il démarre des processus cachés temporaires, effectue les requêtes HTTP, puis les arrête même en cas d'échec. Ce test ne pilote pas le DOM ; vérifier manuellement le formulaire et les changements de saisie dans le navigateur.

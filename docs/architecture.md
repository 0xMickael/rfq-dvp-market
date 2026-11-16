# Comprendre rfq-dvp-market

## Le produit et ses acteurs

Un trader demande un prix à trois market makers simulés pour acheter ou vendre un token tBILL contre un token sEUR. C'est un marché RFQ (*request for quote*) : le trader choisit une offre avant de l'exécuter. Il n'y a ni pool AMM ni order book.

Les deux actifs sont des ERC-20 fictifs à six décimales. Le tBILL illustre un titre court terme, mais ne donne aucun droit réel à remboursement et ne distribue aucun rendement. Le taux utilisé dans le pricing ne crée pas de yield on-chain. L'issuer peut mint pour financer la démo.

Exemple : BUY 100 tBILL à 98,06 sEUR/tBILL. Le trader paie 9 806 sEUR au MM, qui livre 100 tBILL. Pour SELL, les flux sont inversés. Le MM apporte son inventaire et cote un bid inférieur à son ask : le spread peut rémunérer sa fourniture de liquidité, mais ne garantit aucun profit face au risque d'inventaire et aux coûts d'exécution. Aucun frais de plateforme n'est prélevé ; le submitter paie le gas séparément en ETH.

## Un trade de bout en bout

1. Le frontend envoie une RFQ à Go : wallet, BUY/SELL, montant et unité.
2. Go valide la requête et la transmet à Rust. Rust calcule les offres et stocke les termes exacts avec leurs identifiants et échéances. Aucune réservation à ce stade.
3. Le trader choisit une quote. Le frontend compare les termes affichés au message EIP-712 et MetaMask signe ce message, sans transaction.
4. Go vérifie la signature trader. Rust accepte et réserve sous un seul verrou, si la quote reste valide et l'inventaire disponible.
5. Go vérifie la réservation exacte puis signe pour le MM. Les deux signatures autorisent les mêmes montants, contreparties, tokens et deadline.
6. Un worker Go construit et signe la transaction depuis le compte submitter, puis l'envoie à Anvil. Le trader ne paie pas ce gas.
7. `DvPSettlement` vérifie les signatures et échange les deux actifs : les deux transferts réussissent, ou toute l'opération est annulée.
8. Go vérifie le résultat on-chain, puis notifie Rust. Rust comptabilise l'échange une seule fois. Le frontend affiche `SETTLED`, le hash et les nouveaux soldes.

La réservation est un engagement interne de disponibilité ; elle ne déplace pas les tokens et ne remplace pas les signatures. Un retour RPC ou un hash ne prouve pas non plus que le trade est réglé.

## Ce que chaque langage apporte

**Rust possède l'état de marché.** `Arc` partage le moteur entre les handlers ; son `Mutex` protège ensemble RFQs, trades et inventaires. Accepter signifie vérifier puis modifier ces données dans une section critique indivisible. Avec 150 tBILL disponibles, deux BUY concurrents de 100 ne peuvent pas tous deux réserver. `Result` rend les refus explicites, et les enums limitent les transitions possibles. Aucun appel réseau ni `await` n'a lieu sous ce verrou.

**Go orchestre les échanges entre composants.** Il expose l'API, vérifie les signatures et utilise `ethclient` pour Anvil. Une goroutine exécute le worker de settlement sans maintenir ouverte la requête HTTP du trader. `context.Context` borne les appels et propage l'annulation. Un seul worker attribue les nonces du submitter ; un mutex distinct protège sa liste de travaux en mémoire. Go ne recalcule pas les prix.

**Solidity impose les règles au moment des transferts.** Le contrat vérifie l'autorisation des deux contreparties, la deadline et le replay, puis transfère atomiquement. Une allowance permet un prélèvement par le contrat ; les signatures fixent pour quel trade ce prélèvement est autorisé.

**TypeScript relie ces opérations au trader.** Connexion wallet, contrôle du compte/réseau, signature, affichage et polling. La clé trader reste dans le wallet.

Choix de simplicité : deux services applicatifs, HTTP/JSON local au lieu de FFI/gRPC, trois stratégies MM dans Rust et trois signers simulés dans Go. Le verrou global et le worker séquentiel sont lisibles ; les optimiser exigerait d'abord des mesures.

## Prix, unités et arrondis

```text
P_ref = FaceValue / (1 + r * tau)
deviation_i = (inventory_tbill_i - target_i) / target_i
mid_i = P_ref - lambda_i * deviation_i
bid_i = mid_i - spread_i / 2
ask_i = mid_i + spread_i / 2
```

`r` est annualisé, `tau` en années ; prix, spread et lambda sont en sEUR/tBILL. Un MM surchargé en tBILL baisse son prix pour favoriser ses ventes. BUY préfère l'ask le plus bas ; SELL le bid le plus haut. À égalité, l'identifiant MM départage les offres.

Au démarrage : FaceValue 100, r 0,04, tau 0,5 ; P_ref = 98,039215. Les MM ont un target de 1 000 tBILL et lambda = 0,1 : MM1 possède 1 000 tBILL / 100 000 sEUR (spread 0,08), MM2 1 200 / 90 000 (0,06), MM3 800 / 110 000 (0,10). Le meilleur ask initial est 98,049215 chez MM2 ; le meilleur bid 98,009215 chez MM3. Le pricing utilise l'inventaire comptabilisé ; la capacité utilise le disponible après réservations.

Les montants Rust sont des entiers `u64` à six décimales, échelle `S = 1_000_000`, transportés en chaînes JSON. Les intermédiaires utilisent `u128`/`i128` avec contrôle des dépassements. Maximum : `18446744073709.551615`. Les entrées positives acceptent le point décimal, sans signe, espaces, virgule ou notation scientifique. Taux borné à [0,1], maturité à [0,100], nominal et target strictement positifs.

Avec quantité `q`, prix `p` et cash demandé `b` en atomes :

```text
BUY en cash : q = floor(b * S / p)   ; budget maximal
SELL en cash : q = ceil(b * S / p)   ; minimum à recevoir
Cash coté BUY = ceil(q * p / S)
Cash coté SELL = floor(q * p / S)
```

En tBILL, la quantité saisie reste exacte. Un résultat nul est refusé. Le prix de référence est arrondi vers le bas, le skew tronqué vers zéro et le demi-spread arrondi vers le haut. Aucun partial fill ni agrégation de MM : le moteur sert toute la quote calculée ou refuse. Ses montants ne sont jamais recalculés lors de l'acceptation.

## États, échéances et échecs

```text
RFQ : QUOTED -> ACCEPTED
            -> EXPIRED
Trade : RESERVED -> SETTLEMENT_PENDING -> SETTLED
                                     -> SETTLEMENT_FAILED
Disponible = inventaire comptabilisé - réservations >= 0
```

BUY réserve les tBILL sortants du MM ; SELL réserve son cash exact. Les actifs attendus en retour ne sont pas disponibles avant settlement. Une quote peut devenir inexécutable avant son expiration si un autre trader réserve l'inventaire entre-temps.

Deux échéances distinctes : acceptation seulement si `now < quote_expires_at` (120 s après création dans la démo réseau) ; règlement seulement si `block.timestamp < settlement_deadline` (600 s après création). Elles ne sont pas prolongées à l'acceptation. Le domaine garde des defaults `Timing` de 30/300 s pour les tests. L'horloge injectée Rust est lue sous verrou et ne recule pas dans une session.

- **Signature refusée ou quote expirée** : pas de nouvelle réservation ; demander une nouvelle quote.
- **Double acceptation ou réponse HTTP perdue** : Rust refuse une seconde réservation ; Go retrouve le trade existant lors d'un retry valide. Une autre quote de la même RFQ ne crée pas un second trade.
- **RPC indisponible / réponse d'envoi perdue** : garder la réservation, vérifier la chaîne et, si nécessaire, renvoyer exactement la même transaction avec le même nonce. Pas de remplacement automatique ni de hausse du gas price.
- **Simulation refusée ou receipt reverté** : ne pas libérer immédiatement. Les signatures peuvent rester exécutables, notamment après correction d'une allowance.
- **Succès on-chain** : Go vérifie état consommé, événement aux montants exacts, receipt réussi et bloc canonique. Il retrouve aussi un règlement soumis par un tiers. Rust débite la sortie, crédite l'entrée et retire la réservation sous verrou. Un doublon de notification ne recommence pas ces écritures.
- **Échec définitif local** : Go constate un bloc canonique ayant atteint la deadline et un trade non consommé. Si un nonce a déjà été signé, il attend sa consommation pour ne pas bloquer les transactions suivantes. Rust libère la réservation sans crédit entrant.
- **Moteur indisponible** : Go ne fabrique pas de marché de secours. Après perte d'état d'un service, réinitialiser la session entière.

## Confiance et limites

Le DvP ne consulte pas Rust : il garantit les signatures, le replay et l'atomicité, pas l'existence d'une RFQ. L'association au trade réservé repose sur le signer MM Go. Un Go compromis peut signer pour les MM de démo, mais ne peut pas fabriquer la signature du trader. Il peut retarder ou censurer la soumission : sécurité des termes ne signifie pas disponibilité garantie.

Les tokens restent dans les wallets, sans escrow préalable. Une réservation ne protège pas contre des transferts externes ou une révocation d'allowance. Les inventaires Rust ne suivent que les settlements de l'application, pas tous les mouvements de tokens. Les issuers peuvent mint ; l'opérateur Anvil peut réinitialiser la chaîne.

Les routes Rust, notamment les notifications de settlement, font confiance au Go local. Pas d'authentification HTTP, de signers isolés, de reprise durable, de purge globale des travaux, de prise en charge ERC-1271, de finality publique ou de gestion des reorgs après validation. L'ABI DvP suppose les ERC-20 standards fournis, sans frais/rebase. Le worker recherche les événements depuis le bloc 0 : adapté à la petite chaîne de démo.

## Persistance et reprise

**Persister signifie conserver les données après l'arrêt d'un programme**, dans un fichier ou une base. Aujourd'hui, Rust garde RFQs, inventaires et réservations en RAM ; Go garde travaux et transactions en RAM. Les fichiers sources ne sont pas ces données. Le `sessionStorage` du navigateur ne conserve que l'ID du dernier settlement, pas l'état serveur.

Exemple : Rust réserve 100 tBILL. Go envoie la transaction. Anvil la règle, mais Go s'arrête avant de prévenir Rust. La transaction n'est pas annulée parce que Go a redémarré : si la chaîne est conservée, les soldes ont déjà changé. Le nouveau processus a pourtant oublié quel trade il suivait. Réinitialiser seulement les inventaires risquerait de proposer à nouveau des tokens déjà dépensés ou engagés.

Une reprise durable devrait :

1. Sauvegarder les termes acceptés et la réservation Rust avant de confirmer cette transition.
2. Sauvegarder la transaction Go signée et son nonce avant son envoi.
3. Au redémarrage, relire les travaux incomplets, interroger la chaîne et reprendre le traitement sans créer de doublon.
4. Rendre aussi persistante l'application unique du résultat dans l'inventaire.

La persistance garde les données ; la reprise décide quoi faire avec elles. Une base PostgreSQL pourrait fournir le stockage et des transactions locales, mais ne rendrait pas automatiquement atomiques Rust, Go et Ethereum. Il resterait à définir l'ordre des écritures et les retries. Ce mécanisme n'est pas implémenté. La solution actuelle est le [reset local coordonné](development.md#arrêt-et-réinitialisation).

Pour un portfolio, cette limite peut être annoncée dès une première publication. La reprise après crash est un futur sujet d'ingénierie intéressant, pas un prérequis pour montrer le trading local déjà fonctionnel.

## Lire le code dans l'ordre du trade

1. [Interface](../web/src/main.ts), [wallet](../web/src/wallet.ts) et [termes signés](../web/src/terms.ts).
2. [Routes Go](../backend/internal/httpapi/rfqs.go) et [signature](../backend/internal/signing/typed.go).
3. [Pricing Rust](../market-engine/src/domain/pricing.rs), [quotes](../market-engine/src/domain/quote.rs), puis [Engine et transitions](../market-engine/src/domain/engine.rs).
4. [Worker Go](../backend/internal/settlement/manager.go), puis [contrat DvP](../contracts/src/DvPSettlement.sol).
5. [Tests de réservation](../market-engine/tests/reservations.rs), [pannes du submitter](../backend/internal/settlement/manager_test.go) et [scénario Anvil](../web/tests/live-settlement.mjs).

Le schéma signé exact et les routes sont regroupés dans [api.md](api.md). Les décisions de portée restent simples : Anvil avant Sepolia, BUY/SELL ensemble, entiers exacts, full fills, EIP-712 dès le prototype, état en mémoire pour cette version. Pas d'order book, multi-chain, bridges, ZK, account abstraction ou complexité quantitative ajoutés à cette démo.

## Scénarios de panne reproductibles

La commande scripts/verify.ps1 exécute les tests ci-dessous. Les tests RPC simulés ne démontrent pas une reprise après crash.

| Déclencheur | Comportement et invariant | Récupération | Test |
|---|---|---|---|
| Deux acceptations dépassent le stock | Une seule réserve ; disponible positif ou nul | Nouvelle RFQ pour le perdant | Rust reservations : concurrent_requests_cannot_reserve_200_against_150_in_either_asset ; intégration Signatures |
| Double acceptation | Un seul trade et une seule réservation | Retry de la même acceptation | Rust concurrent_double_acceptance_and_sibling_quote_are_rejected ; intégrations |
| Acceptation à expiration exacte | Refus dès now >= expires_at | Nouvelle RFQ | Rust exact_expiry_is_exclusive_and_time_never_releases_reservations ; Foundry testDeadlineBoundary pour la deadline on-chain |
| Réponse RPC perdue | Réservation conservée ; mêmes octets et nonce | Lecture de la chaîne puis retry sûr | Go TestLostRPCResponseAndRustNotificationKeepSameTransaction et TestLostResponseAfterInclusionDoesNotResubmit : RPC simulé |
| Notification répétée | Pas de double comptabilisation | Répéter la notification identique | Rust settlement_is_atomic_and_idempotent_for_both_sides ; intégration Settlement sur vrais processus |
| Allowance absente | Aucun transfert, réservation conservée avant deadline | Résolution après preuve on-chain de deadline dépassée | Intégration Settlement ; snapshot final identique au snapshot avant échec |

Les avertissements du linter Foundry sont visibles : les transferFrom au nom des contreparties sont autorisés par leurs signatures ; block.timestamp définit volontairement la deadline ; l’événement après transfert est protégé par nonReentrant et le rollback transactionnel. Le warning de lecture de chain ID concerne le test de changement de domaine. Ces justifications ne constituent pas un audit externe.

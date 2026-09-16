# Modèle de marché

## État actuel

Le domaine Rust implémente la cotation BUY et SELL sur un snapshot d'inventaires, avec trois MM de démonstration. Le formulaire y accède via Go et HTTP/JSON. Il ne crée ni RFQ stockée, ni réservation, ni transfert. Les tokens et le DvP sont testés séparément sur Anvil. Voir les validations dans [progress.md](progress.md).

## Actifs et flux cibles

`sEUR` et `tBILL` seront deux ERC-20 fictifs. `tBILL` n'est ni un vault ni une créance juridiquement remboursable. La formule de prix ne crée aucun mécanisme de remboursement à maturité et aucun yield distribué.

- BUY : le trader paie du sEUR au MM et reçoit du tBILL. Le meilleur prix est le plus petit ask.
- SELL : le trader livre du tBILL au MM et reçoit du sEUR. Le meilleur prix est le plus grand bid.

Exemple : BUY 100 tBILL à 98,06 sEUR par tBILL implique 9 806 sEUR du trader vers le MM et 100 tBILL dans le sens inverse. Un full fill exécute toute la quantité ; aucun partial fill initial.

## Pricing déterministe

```text
P_ref = FaceValue / (1 + r * tau)
deviation_i = (inventory_i - target_i) / target_i
mid_i = P_ref - lambda_i * deviation_i
bid_i = mid_i - spread_i / 2
ask_i = mid_i + spread_i / 2
```

`FaceValue`, `P_ref`, `lambda` et le spread sont exprimés en sEUR par tBILL ; `r` est un taux annualisé et `tau` une durée en années. Le target d'inventaire doit être strictement positif. Pour FaceValue = 100, r = 0,04 et tau = 0,5, P_ref vaut environ 98,0392.

Un MM surchargé en tBILL abaisse son mid : il devient plus attractif à l'achat pour les traders et moins agressif pour acquérir encore du tBILL. Le spread vise à rémunérer la fourniture de liquidité ; ce n'est pas un profit garanti. Le MM supporte le risque d'inventaire, les mouvements du prix de référence et les coûts d'exécution.

`Market::demo()` utilise FaceValue = 100, r = 0,04 et tau = 0,5, soit P_ref = 98,039215. Les trois MM ont un target de 1 000 tBILL, lambda = 0,1 et aucune réservation initiale :

- MM1 : 1 000 tBILL, 100 000 sEUR, spread 0,08.
- MM2 : 1 200 tBILL, 90 000 sEUR, spread 0,06.
- MM3 : 800 tBILL, 110 000 sEUR, spread 0,10.

Le meilleur ask est celui de MM2 (98,049215) ; le meilleur bid est celui de MM3 (98,009215). À prix égal, l'identifiant MM départage les quotes par ordre lexicographique. Aucun prix aléatoire, oracle ou service MM indépendant.

## Quantités et inventaire

Les calculs utilisent `Amount`, un entier `u64` à six décimales (`S = 1_000_000`), jamais des flottants binaires. La même échelle représente quantités, cash, prix, taux et maturité ; les champs indiquent leurs unités. La cible ERC-20 est également de six décimales pour chaque actif. Les futurs montants JSON seront des chaînes ; Go transportera les montants calculés par Rust sans les recalculer.

La saisie accepte au plus six décimales, sans signe, espace, virgule ou notation scientifique. Le maximum représentable est `18446744073709.551615` ; une demande nulle est refusée. Le taux est borné à [0, 1], la maturité à [0, 100] ans. Le nominal, le target et les prix doivent être strictement positifs. Les produits intermédiaires utilisent `u128` ou `i128` avec contrôle des dépassements ; une valeur non représentable est refusée.

Le prix de référence est arrondi vers le bas. Le skew d'inventaire est calculé en une division, tronquée vers zéro. Le demi-spread est arrondi vers le haut : un spread impair en atomes produit un spread effectif supérieur d'un atome.

### Montant libre et arrondis

BUY et SELL sont développés ensemble. Le trader choisit l'unité de saisie :

- tBILL : quantité exacte, éventuellement fractionnaire.
- sEUR pour BUY : budget maximal, avec la plus grande quantité représentable respectant ce budget.
- sEUR pour SELL : montant minimal à recevoir, avec la plus petite quantité représentable permettant de l'atteindre.

Avec `q`, `p` et `b` exprimés en atomes (quantité, prix et cash demandé) :

```text
BUY cash : q = floor(b * S / p)
SELL cash : q = ceil(b * S / p)
Cash réellement coté BUY = ceil(q * p / S)
Cash réellement coté SELL = floor(q * p / S)
```

À un prix de 3 sEUR/tBILL, BUY avec un budget de 10 cote 3,333333 tBILL pour 9,999999 sEUR ; SELL avec un objectif de 10 cote 3,333334 tBILL pour 10,000002 sEUR. L'écart vient de la granularité, pas d'un partial fill. Quantité ou cash arrondi à zéro : refus explicite.

Un MM doit servir toute la quote calculée. Le moteur ne réduit pas la demande pour l'adapter à son inventaire et n'agrège pas plusieurs MM. Il retourne les quotes possibles et les refus par MM ; sans quote possible, il retourne une erreur explicite. Les futures fixtures interlangages devront préserver exactement ces montants.

Pour chaque actif d'un MM :

```text
available = accounted - reserved
reserved >= 0
available >= 0
```

`accounted` représente les avoirs réglés connus du moteur ; `reserved` représente des sorties déjà engagées. Un BUY réserve du tBILL du MM ; un SELL réserve son cash. Les entrées attendues d'un settlement pending ne sont pas dépensables. Le pricing initial utilise l'inventaire tBILL comptabilisé ; la capacité d'exécuter utilise le disponible.

Le domaine actuel vérifie `available` sur le snapshot fourni et ne le modifie pas. Le champ `reserved` permet de tenir compte de sorties déjà engagées dans ce snapshot, sans implémenter leur création. Aucune protection contre deux acceptations concurrentes n'est donc encore livrée.

Dans la future slice, une quote ne réservera pas d'inventaire. Son acceptation pourra être refusée si une autre quote a consommé la capacité entre-temps. Une acceptation réussie devra consommer la quote et réserver dans une seule transition Rust, avec une seule quote acceptée par RFQ.

Le solde on-chain peut diverger de la comptabilité si un wallet transfère des tokens hors rfq-dvp-market. Les comptes MM de démonstration sont dédiés à la session. Une réservation Rust ne garantit ni le solde ni l'allowance du trader ; une insuffisance peut entraîner un revert atomique.

## Reporté

Pricing dépendant de la taille, maturité évolutive, données de marché externes, remboursement, partial fills et stratégies quantitatives avancées. Les soldes et allowances du trader ne sont pas vérifiés par la cotation actuelle.

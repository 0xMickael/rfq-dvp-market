# Modèle de marché

## État actuel

Aucun pricing, inventaire, token ou RFQ n'est implémenté. Les règles ci-dessous décrivent la cible de la première slice ; les paramètres chiffrés d'exécution seront fixés avant son implémentation.

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

MM1, MM2 et MM3 seront trois configurations du moteur, avec inventories cash/titre, target, lambda et spread distincts. Aucun prix aléatoire et aucun service MM indépendant. Un snapshot de référence configuré pour la session suffit ; pas d'oracle ni d'intégration de taux externe.

## Quantités et inventaire

Les calculs monétaires utiliseront des entiers à échelle fixe, jamais des flottants binaires. Les montants JSON seront des chaînes pour préserver les entiers au-delà de la précision de `Number` en JavaScript. Go transporte les montants calculés par Rust ; le frontend ne redéfinit pas le montant signé.

Avant de coder le pricing, fixer conjointement les décimales des tokens, les échelles du prix et du taux, la règle d'arrondi du cash, les limites de quantité/prix et le contrôle des overflows. Ces paramètres ne sont volontairement pas inventés dans ce skeleton. Les futures fixtures partagées devront produire exactement les mêmes montants dans Rust, Go, TypeScript et Solidity.

Pour chaque actif d'un MM :

```text
available = accounted - reserved
reserved >= 0
available >= 0
```

`accounted` représente les avoirs réglés connus du moteur ; `reserved` représente des sorties déjà engagées. Un BUY réserve du tBILL du MM ; un SELL réserve son cash. Les entrées attendues d'un settlement pending ne sont pas dépensables. Le pricing initial utilise l'inventaire tBILL comptabilisé ; la capacité d'exécuter utilise le disponible.

Une quote ne réserve pas encore d'inventaire. Même avant son expiry, son acceptation peut donc être refusée si une autre quote a consommé la capacité entre-temps. Une acceptation réussie consomme la quote et réserve l'inventaire dans une seule transition Rust. Une seule quote est acceptée par RFQ.

Le solde on-chain peut diverger de la comptabilité si un wallet transfère des tokens hors rfq-dvp-market. Les comptes MM de démonstration sont dédiés à la session. Une réservation Rust ne garantit ni le solde ni l'allowance du trader ; une insuffisance peut entraîner un revert atomique.

## Reporté

Pricing dépendant de la taille, maturité évolutive, données de marché externes, remboursement, partial fills et stratégies quantitatives avancées. BUY sera implémenté avant SELL, qui reste dans la phase 1.

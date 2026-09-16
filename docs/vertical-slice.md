# Première vertical slice : BUY 100 tBILL

## État actuel

Seul le skeleton est livré. Ce document est un plan de travail futur ; aucune des opérations métier ci-dessous n'est disponible.

## Préparer les contrats et les fixtures

1. Fixer les unités, arrondis, bornes et paramètres MM décrits dans le [modèle de marché](market-model.md).
2. Définir les termes exacts d'un trade signé et une fixture EIP-712 partagée entre Solidity, Go et TypeScript. Les montants attendus doivent correspondre au calcul Rust.
3. Ajouter OpenZeppelin épinglé, `SEUR`, `TBILL` et `DvPSettlement`. Tester les permissions de mint et les invariants du DvP avec Foundry.
4. Déployer sur une instance Anvil dédiée et vierge, financer un trader, trois MM et le compte submitter, puis préparer les allowances.

## Traverser les frontières une fois

1. Le trader connecte son EOA, voit ses soldes et demande BUY 100 via Go.
2. Go valide la forme de la demande et la transmet à Rust.
3. Rust calcule les trois quotes à partir du même snapshot de référence et identifie le meilleur ask. Les quotes sont destinées au trader demandeur, sans réservation préalable.
4. Le frontend affiche quantité, prix, montant cash, contrepartie et échéances. Le trader signe les termes exacts de la quote choisie.
5. Go vérifie la signature ; Rust revalide les termes et accepte/réserve atomiquement si l'inventaire et le temps le permettent.
6. Le signer MM signe les mêmes termes après la réservation. Go ne recalcule ni le cash ni le prix.
7. Le submitter Go construit et soumet la transaction DvP, puis suit son hash. La requête d'acceptation ne reste pas bloquée jusqu'au minage ; le frontend consulte le statut par polling.
8. Le contrat vérifie les deux signatures, le domaine, les montants, les contreparties, la deadline et l'identifiant non consommé. Il transfère cash et titres atomiquement.
9. Go observe le receipt et la preuve du settlement attendu. Rust applique le résultat une seule fois et met à jour ses inventaires. Le frontend affiche `SETTLED` et le transaction hash.

## Critères d'acceptation à implémenter

- BUY 100 : diminution exacte du cash du trader et du tBILL du MM, augmentations symétriques chez les contreparties ; le gas est payé séparément en ETH par le submitter.
- Meilleur ask sélectionné par Rust ; trois quotes issues d'un modèle déterministe, sans prix aléatoires.
- Deux acceptations concurrentes de 100 pour 150 disponibles : une seule réussit. Tester aussi la consommation unique de la même quote/RFQ.
- Acceptation à `now == quote_expires_at` refusée ; settlement à `block.timestamp == settlement_deadline` refusé.
- Modifier montant, token, contrepartie, deadline, identifiant ou domaine invalide l'autorisation.
- Deux soumissions du même trade ne produisent qu'un settlement, y compris avec deux nonces Ethereum différents.
- Faire échouer le second transfert annule le premier et la consommation du trade.
- Timeout RPC : aucune libération prématurée ; résultat réconcilié avec la chaîne.
- Notification de succès répétée : aucune double mise à jour des inventaires.
- Perte d'état : aucune promesse de reprise ; procédure de remise à zéro documentée et suivie.

Les tests métier viseront les invariants et les frontières entre composants. Aucun framework E2E généraliste ne sera ajouté avant d'en avoir besoin ; un petit scénario local reproductible suffit au premier jalon.

## Étape suivante et reports

Ajouter le flow SELL avec sélection du meilleur bid et réservation du cash MM, puis les tests symétriques. SELL termine la phase 1 ; ce n'est pas un ajout de phase 2.

La persistance, les retries durables, le loadgen, le dashboard et le déploiement public ne font pas partie de ce premier flow BUY.

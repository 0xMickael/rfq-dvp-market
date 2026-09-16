# Première vertical slice : BUY et SELL, montants variables

## État actuel

La cotation frontend/Go/Rust et les contrats DvP sont livrés et testés séparément. Les scripts Foundry règlent BUY et SELL sur Anvil ; l'application ne soumet pas encore de transaction. Le prochain jalon porte sur les RFQs, leur expiry et l'acceptation/réservation atomique Rust, avant le raccord des signatures et du submitter Go. Voir [progress.md](progress.md).

## Préparer les contrats et les fixtures

Ces préparatifs sont réalisés côté Solidity/Cast ; la fixture reste à reprendre dans les futurs signers Go et TypeScript. Voir [settlement.md](settlement.md).

1. Reprendre les unités, arrondis, bornes et paramètres MM déjà implémentés dans le [modèle de marché](market-model.md).
2. Définir les termes exacts d'un trade signé et une fixture EIP-712 partagée entre Solidity, Go et TypeScript. Les montants attendus doivent correspondre au calcul Rust.
3. Ajouter OpenZeppelin épinglé, `SEUR`, `TBILL` et `DvPSettlement`. Tester les permissions de mint et les invariants du DvP avec Foundry.
4. Déployer sur une instance Anvil dédiée et vierge, financer un trader, trois MM et le compte submitter, puis préparer les allowances.

## Traverser les frontières une fois

1. Le trader connecte son EOA, voit ses soldes et demande BUY ou SELL via Go, en saisissant une quantité tBILL ou un montant sEUR. BUY 100 reste un scénario de test.
2. Go valide la forme de la demande et la transmet à Rust.
3. Rust calcule les quotes possibles des trois MM à partir du même snapshot et identifie le meilleur ask pour BUY ou bid pour SELL. Les refus sont explicites. Les quotes sont destinées au trader demandeur, sans réservation préalable.
4. Le frontend affiche quantité, prix, montant cash, contrepartie et échéances. Le trader signe les termes exacts de la quote choisie.
5. Go vérifie la signature ; Rust revalide les termes et accepte/réserve atomiquement si l'inventaire et le temps le permettent.
6. Le signer MM signe les mêmes termes après la réservation. Go ne recalcule ni le cash ni le prix.
7. Le submitter Go construit et soumet la transaction DvP, puis suit son hash. La requête d'acceptation ne reste pas bloquée jusqu'au minage ; le frontend consulte le statut par polling.
8. Le contrat vérifie les deux signatures, le domaine, les montants, les contreparties, la deadline et l'identifiant non consommé. Il transfère cash et titres atomiquement.
9. Go observe le receipt et la preuve du settlement attendu. Rust applique le résultat une seule fois et met à jour ses inventaires. Le frontend affiche `SETTLED` et le transaction hash.

## Critères d'acceptation à implémenter

- BUY 100 : diminution exacte du cash du trader et du tBILL du MM, augmentations symétriques chez les contreparties ; le gas est payé séparément en ETH par le submitter.
- SELL : flux inverses, meilleur bid et réservation du cash MM. Tester les deux sens avec saisie tBILL et cash, y compris les fractions et les refus.
- Meilleur ask/bid sélectionné par Rust ; quotes issues d'un modèle déterministe, sans prix aléatoires.
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

Conserver BUY et SELL ensemble à chaque jalon : cotation réseau, contrats signés, acceptation/réservation, puis settlement et affichage des résultats. Les tests de concurrence ne sont pas reportés à la phase 2.

La persistance, les retries durables, le loadgen, le dashboard et le déploiement public ne font pas partie de cette première slice.

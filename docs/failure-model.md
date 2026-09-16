# Échecs et récupération

## État actuel

Seuls les serveurs de santé et la page statique existent. Les scénarios métier suivants sont des exigences à tester pendant la première slice, pas des garanties déjà implémentées.

Un port occupé empêche le serveur concerné de démarrer ; aucun changement silencieux de port n'est prévu. Les health endpoints ne dépendent ni de l'autre serveur ni d'Anvil.

## Scénarios cibles

### Acceptations concurrentes et inventaire épuisé

- Échec : deux BUY de 100 visent un MM disposant de 150 tBILL.
- Attendu : une seule réservation réussit ; l'autre acceptation reçoit un refus explicite.
- Récupération : demander une nouvelle quote ou réduire la quantité.
- Invariant : `available >= 0`, aucun trade accepté sans réservation.

Pour SELL, appliquer la même vérification au cash du MM. Ne pas compter une entrée de cash encore pending comme disponible.

### Doublon ou réponse HTTP perdue

- Échec : le client répète une acceptation après timeout alors que Rust a déjà réservé.
- Attendu : retrouver le résultat avec l'identifiant connu ; aucun nouveau trade ni double réservation.
- Récupération : consulter le trade/RFQ avant toute nouvelle tentative.
- Invariant : une RFQ acceptée au plus une fois ; un résultat de settlement appliqué au plus une fois.

Le contrat possède sa propre protection contre le replay. Une nouvelle requête HTTP ou un nouveau nonce Ethereum ne doivent pas permettre un second règlement du même trade signé.

### Quote expirée ou signature refusée

- Échec : la signature arrive après l'expiry, ou le trader refuse de signer.
- Attendu : aucune acceptation après `now >= quote_expires_at`. La signature trader précède la réservation, donc un refus wallet initial ne bloque pas d'inventaire.
- Récupération : demander une nouvelle quote.
- Invariant : aucun trade accepté avec une quote périmée.

### Moteur Rust indisponible

- Échec : Go ne peut pas joindre Rust ou ne reçoit pas sa réponse.
- Attendu : erreur d'indisponibilité ou résultat incertain explicite. Go ne fabrique aucune quote et ne reconstitue pas un inventaire supposé.
- Récupération : si Rust conserve son état, consulter le résultat de l'opération ; s'il a redémarré et perdu cet état, réinitialiser la session.
- Invariant : aucune décision métier prise par un fallback Go.

### RPC indisponible, transaction bloquée ou résultat inconnu

- Échec : timeout d'envoi, connexion coupée ou absence de receipt.
- Attendu : conserver la réservation et l'état pending. L'absence de receipt ne prouve ni le rejet ni le succès.
- Récupération : Go connaît le hash calculé avant l'envoi et consulte la transaction et l'état du contrat. Pas de remplacement automatique dans la première slice.
- Invariant : aucune libération tant qu'un settlement signé peut encore réussir.

### Revert ou échec du signer MM

- Échec : fonds/allowance insuffisants, deadline dépassée, signature invalide, ou impossibilité d'obtenir la signature MM après réservation.
- Attendu : aucun transfert partiel. Ne pas assimiler une tentative revertée à l'annulation du trade signé.
- Récupération : si l'absence de toute autorisation exécutable est certaine, une réservation non soumise peut être libérée. Sinon, attendre une preuve on-chain d'expiration et vérifier la non-consommation du trade. En cas de doute, conserver la réservation.
- Invariant : ne pas remettre à disposition des actifs encore engagés par des signatures valides.

### Settlement réussi mais notification Rust perdue

- Échec : le receipt confirme le DvP, mais Rust ne reçoit pas le résultat.
- Attendu : Rust reste conservateur ; aucune libération comme s'il s'agissait d'un échec.
- Récupération : Go renvoie le résultat identifiable après retour du moteur, si la session mémoire est intacte. La transition doit tolérer ce doublon.
- Invariant : ne jamais comptabiliser deux fois une entrée ou une sortie.

### Redémarrage Go ou Rust avec perte d'état

- Échec : perte d'une réservation, d'un trade ou du suivi d'une transaction.
- Attendu : ne pas reprendre le trading sur les soldes et la chaîne de l'ancienne session.
- Récupération locale : arrêter frontend, Go, Rust et l'instance Anvil dédiée ; redémarrer une chaîne vierge sans charger d'état ; redéployer et financer les comptes ; redémarrer les services ; vider l'ancien état de la page. Aucun script destructif automatique n'est livré.
- Invariant : aucun nouvel inventaire initial sur une chaîne où d'anciens trades peuvent encore être réglés.

Cette procédure est acceptable pour une démo jetable. Elle ne constitue pas un mécanisme de recovery de production.

## Reporté

PostgreSQL indisponible, crash recovery durable, queue persistante, retries automatisés, transactions remplacées et reorgs seront traités en phase 2/3. La première slice documentera ses limites plutôt que de promettre une disponibilité qu'elle ne garantit pas.

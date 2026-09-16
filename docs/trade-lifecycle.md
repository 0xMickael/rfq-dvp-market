# Lifecycle des RFQs, trades et settlements

## État actuel

Le domaine Rust calcule des quotes à partir d'un snapshot d'inventaires, sans le modifier. Aucun état RFQ/trade ni transition d'acceptation n'existe. Les noms ci-dessous sont un modèle de travail pour la première slice, pas des enums ou un contrat d'API déjà publiés.

## RFQ et acceptation cibles

```text
RFQ : CREATED -> QUOTED -> ACCEPTED
                       -> EXPIRED

Acceptation réussie : vérifier + consommer la quote + créer le trade + réserver
                     dans UNE transition atomique du moteur Rust.
```

Le calcul des trois quotes est local et synchrone au départ ; `QUOTING` n'a pas besoin d'être un état durable distinct. L'absence de quote exécutable est un résultat métier explicite. Une acceptation rejetée ne crée pas de trade et ne consomme pas d'inventaire.

Le trader signe les termes exacts associés à la quote avant l'acceptation. Au moment de la transition, Rust vérifie à nouveau la quote, le wallet destinataire, l'expiry, l'absence d'acceptation précédente et l'inventaire disponible. La signature ne suffit pas à garantir la disponibilité. Le MM ne signe les mêmes termes qu'après réservation réussie ; le submitter ne doit pas détenir deux signatures exécutables pour une simple quote non réservée.

## Trade et tentative de settlement

```text
Trade : RESERVED -> SETTLEMENT_PENDING -> SETTLED
                  -> SETTLEMENT_FAILED, uniquement après résolution sûre
```

Le statut du trade est distinct du statut de chaque tentative Ethereum. Une tentative peut être soumise, incluse, revertée ou d'issue inconnue. Une requête RPC acceptée n'est pas une preuve d'inclusion. Un revert ne constitue pas à lui seul une preuve que le trade signé ne peut plus être réglé.

Go associe le transaction hash au trade et observe le résultat. Rust applique chaque résultat une seule fois. Sur Anvil avec automining, un receipt réussi dans la chaîne canonique, accompagné de l'événement/état de settlement attendu, suffit au jalon local. Cette convention n'est pas une politique de finality pour Sepolia.

Sur succès, Rust débite la sortie comptabilisée, enlève la réservation et crédite l'actif reçu, en une transition. Exemple BUY : cash MM augmenté, tBILL MM diminué, réservation tBILL supprimée. Le répéter ne doit pas doubler la comptabilité.

## Deux échéances distinctes

- `quote_expires_at` : Rust autorise l'acceptation uniquement si `now < quote_expires_at`.
- `settlement_deadline` : le contrat autorise le règlement uniquement si `block.timestamp < settlement_deadline`.

La seconde échéance est signée, connue lors de l'acceptation et postérieure à la première. Les durées exactes seront fixées avec le flow wallet. L'horloge locale sert à l'acceptation ; le timestamp de bloc sert au contrat. Les tests du moteur doivent pouvoir fournir explicitement le temps.

L'expiration commerciale de la quote ne libère pas une réservation déjà acceptée. Après émission des signatures, un timeout local ne suffit pas non plus. Sur Anvil, une libération sûre peut s'appuyer sur un bloc canonique ayant atteint la deadline et sur la vérification que le trade n'a pas été consommé. Si le trade a été réglé, appliquer le succès au lieu de libérer comme s'il avait échoué.

Sans preuve suffisante, conserver le trade pending et la réservation. La sûreté prime alors sur la disponibilité ; voir le [modèle d'échec](failure-model.md).

## Doublons et concurrence

Deux acceptations concurrentes ne doivent jamais créer deux trades pour la même quote/RFQ. Un doublon identifiable renvoie le résultat existant ou une erreur explicite sans nouvelle réservation. Les retours de settlement sont également idempotents au niveau de la transition.

Cette protection métier est nécessaire dès la slice. Un stockage durable des clés d'idempotency HTTP, une outbox et la reprise des workers appartiennent à la phase 2.

## Reporté

Annulation on-chain, remplacement automatique de transactions, reprise après crash, reorgs et partial fills. Après perte d'état en phase 1, appliquer la réinitialisation locale coordonnée ; ne jamais recréer simplement les inventaires sur une chaîne conservant d'anciens settlements.

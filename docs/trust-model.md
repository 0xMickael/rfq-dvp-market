# Modèle de confiance

## État actuel

L'application de cotation ne détient aucune clé et ne connecte aucun wallet. Les scripts Foundry de test déploient et règlent des trades sur Anvil avec son mnemonic public. Les contrats vérifient deux signatures, deadline, replay et atomicité ; leur raccord à la réservation Rust reste à construire. Voir [settlement.md](settlement.md). Les API locales ne sont pas authentifiées.

## Acteurs et pouvoirs dans la première slice

- Le trader utilise un wallet EOA local et signe les termes qu'il accepte. Sa clé reste dans son wallet.
- Les trois MM sont des stratégies simulées par Rust ; leurs clés EOA de test seront détenues par des signers dans Go.
- Le submitter Go possède un compte distinct qui paie le gas, transmet les termes et les signatures, puis suit la transaction.
- Les issuers peuvent mint les actifs fictifs pour initialiser la démo. Cela ne représente pas de l'argent ou un titre réel.
- Anvil et son opérateur contrôlent la chaîne de démonstration, y compris sa remise à zéro.

Les modules signer et submitter sont séparés pour clarifier les responsabilités, mais leur hébergement dans le même processus n'est pas une isolation de sécurité. Un Go compromis peut signer pour les MM simulés ; il ne peut pas fabriquer la signature du trader. Une infrastructure de MM indépendants n'est pas revendiquée.

## Autorisation des termes et DvP

Les deux contreparties signeront le même message EIP-712, engageant au minimum l'identifiant unique du trade, les contreparties, les tokens, les quantités exactes et la deadline. Le domaine sera lié à `chainId` et `verifyingContract`, avec un nom et une version explicites.

Le contrat utilisera les primitives OpenZeppelin pour EIP-712/ECDSA et les transferts ERC-20. Il vérifiera les deux signers, les termes autorisés, la deadline et la non-consommation de l'identifiant. Cet identifiant sera consommé atomiquement avec les transferts : un revert annule aussi cette consommation.

Une allowance ERC-20 permet au contrat de prélever un token ; elle n'autorise pas un prix ou une contrepartie donnés. Les signatures assurent cette liaison. EIP-712 ne fournit pas lui-même la protection contre le replay : elle appartient au contrat rfq-dvp-market.

Le settlement agent ne peut modifier un montant accepté sans invalider la signature du trader. Il peut néanmoins retarder ou censurer une soumission. Une signature n'assure donc pas la liveness.

Le contrat prouve l'autorisation cryptographique et le règlement atomique ; il ne prouve pas qu'une entrée RFQ existe dans la mémoire de Rust. L'association avec un trade accepté repose sur le flow de réservation et sur la politique du signer MM, qui ne signe qu'après cette réservation. Les clés MM simulées restent une hypothèse de confiance.

## Limites de l'inventaire off-chain

Les actifs restent dans les wallets jusqu'au settlement ; il n'y a pas d'escrow préalable. Une réservation protège la cohérence interne de rfq-dvp-market, pas les soldes contre des transferts externes. Le trader peut manquer de fonds ou révoquer une allowance : le DvP doit alors revert sans transfert partiel.

Les comptes MM de démo sont dédiés à rfq-dvp-market. Un changement de solde externe invalide les hypothèses de la session ; il faut arrêter les nouvelles acceptations et résoudre/réinitialiser la démo, sans inventer du disponible.

## Reporté

Authentification de l'API, sécurité des communications interservices, signers isolés, custody, smart contract wallets, reorgs et finality publique. Les signatures de trades ne remplacent pas une authentification de toutes les actions HTTP.

## Références

- [EIP-712 : typed structured data](https://eips.ethereum.org/EIPS/eip-712)
- [OpenZeppelin : EIP712 et ECDSA](https://docs.openzeppelin.com/contracts/5.x/api/utils/cryptography)

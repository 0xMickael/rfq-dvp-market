# Déploiements locaux

`DeployLocal.s.sol` écrit `31337.json` (ignoré par Git). Le fichier contient les adresses publiques, jamais les clés. Une simulation peut aussi écrire ce fichier : il ne prouve pas à lui seul la réussite d'un broadcast. Vérifier les receipts et le code aux adresses. Redéployer après une remise à zéro d'Anvil.

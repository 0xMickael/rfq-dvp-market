# Scripts Anvil

`DeployLocal.s.sol` déploie les trois contrats, finance les wallets et prépare leurs allowances. Index du mnemonic public Anvil : 0 issuer, 1 trader, 2 MM1, 3 MM2, 4 MM3, 5 submitter.

`SettleLocal.s.sol` lit `deployments/31337.json`, puis soumet BUY 100 et SELL 100. Ses identifiants fixes empêchent de rejouer le scénario sur le même déploiement. Ce script ne remplace pas le futur submitter Go.

Chain ID imposé : 31337. Utiliser exclusivement le RPC local Anvil et ses comptes de démonstration. Voir [development.md](../../docs/development.md).

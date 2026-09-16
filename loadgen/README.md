# Load generator — phase 2

Ce répertoire est uniquement documentaire. Aucun binaire ni module Go n'est créé à ce stade.

Le futur outil simulera des traders indépendants via l'API Go : BUY/SELL, quantités variables, quotes acceptées ou ignorées et requêtes concurrentes. Il mesurera les résultats et latences vus par le client (throughput, p50, p95, p99).

Il sera distinct du frontend et du monitoring Prometheus/Grafana. Sa réalisation attend la première slice et le travail de robustesse de phase 2.

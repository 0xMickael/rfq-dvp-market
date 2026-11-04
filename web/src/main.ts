import { BrowserProvider } from 'ethers';
import type { Eip1193Provider } from 'ethers';
import { digest, typedTrade, verifySignatures } from './terms';
import type { Deployment, Quote, Rfq, TypedTrade } from './terms';
import { checkWallet, connectWallet, readWallet, walletError } from './wallet';

type Injected = Eip1193Provider & { on?: (event: string, handler: () => void) => void };
const injected = (window as Window & { ethereum?: Injected }).ethereum;
const form = document.querySelector<HTMLFormElement>('#quote-form')!;
const side = document.querySelector<HTMLSelectElement>('#side')!;
const unit = document.querySelector<HTMLSelectElement>('#unit')!;
const amount = document.querySelector<HTMLInputElement>('#amount')!;
const status = document.querySelector<HTMLParagraphElement>('#frontend-status')!;
const quotes = document.querySelector<HTMLUListElement>('#quotes')!;
const rejections = document.querySelector<HTMLUListElement>('#rejections')!;
const help = document.querySelector<HTMLParagraphElement>('#amount-help')!;
const amountLabel = document.querySelector<HTMLSpanElement>('#amount-label')!;
const connect = document.querySelector<HTMLButtonElement>('#connect')!;
const walletStatus = document.querySelector<HTMLParagraphElement>('#wallet-status')!;
const tradeStatus = document.querySelector<HTMLParagraphElement>('#trade-status')!;
const retry = document.querySelector<HTMLButtonElement>('#retry')!;
let account = '';
let config: Deployment | undefined;
let epoch = 0;
let busy = false;
let connecting = false;
let walletRevision = 0;
let activeRfq: Rfq | undefined;
let pending: { rfq: Rfq; quote: Quote; data: TypedTrade; signature: string } | undefined;
let settlementEnabled = false;
let trackedTrade = sessionStorage.getItem('settlement-trade') ?? '';
let polling = false;
const balances = document.querySelector<HTMLParagraphElement>('#balances')!;
type Settlement = { trade_id: string; status: string; transaction_hash?: string; message: string };

async function refreshBalances() {
  const selected = account;
  if (!selected || !settlementEnabled) { balances.textContent = ''; return; }
  try {
    const result = await api<{ sEUR: string; tBILL: string }>(`/api/balances/${selected}`);
    if (account === selected) balances.textContent = `Soldes : ${result.sEUR} sEUR · ${result.tBILL} tBILL`;
  } catch { if (account === selected) balances.textContent = 'Soldes Anvil indisponibles.'; }
}
async function pollSettlement() {
  if (!trackedTrade || polling) return;
  polling = true;
  try {
    const result = await api<Settlement>(`/api/settlements/${trackedTrade}`);
    tradeStatus.textContent = `${result.status} — ${result.message}${result.transaction_hash ? ` Transaction : ${result.transaction_hash}` : ''}`;
    if (result.status === 'SETTLED' || result.status === 'SETTLEMENT_FAILED') {
      trackedTrade = ''; sessionStorage.removeItem('settlement-trade');
      await refreshBalances(); refreshExpiry();
    }
  } catch (error) { tradeStatus.textContent = `Suivi interrompu : ${String(error)} Le résultat du trade reste à vérifier.`; }
  finally { polling = false; }
}
setInterval(() => { void pollSettlement(); }, 1500);
setInterval(() => { void refreshBalances(); }, 10000);

class APIError extends Error {
  constructor(message: string, readonly code: string, readonly rejections: { maker_id: string; message: string }[] = []) { super(message); }
}
function showRejections(items: { maker_id: string; message: string }[]) {
  for (const rejection of items) { const item = document.createElement('li'); item.textContent = `${rejection.maker_id} : ${rejection.message}`; rejections.append(item); }
}

async function api<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, { method: body === undefined ? 'GET' : 'POST', headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(5000), cache: 'no-store' });
  const result = await response.json();
  if (!response.ok) throw new APIError(result.message ?? 'Requête refusée.', result.code, result.rejections ?? []);
  return result as T;
}
function lock(value: boolean) {
  busy = value;
  for (const input of form.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLButtonElement>('input,select,button')) input.disabled = value;
  connect.disabled = value; retry.disabled = value;
  refreshExpiry();
}
function clear() {
  epoch++; activeRfq = undefined;
  quotes.replaceChildren(); rejections.replaceChildren(); status.textContent = '';
  const buying = side.value === 'BUY';
  unit.options[1].textContent = buying ? 'Un budget maximal en sEUR' : 'Un montant minimum de sEUR à recevoir';
  amountLabel.textContent = unit.value === 'tBILL' ? `Quantité de tBILL à ${buying ? 'acheter' : 'vendre'}` : buying ? 'Budget maximal à dépenser (sEUR)' : 'Cash minimum à recevoir (sEUR)';
  help.textContent = unit.value === 'tBILL' ? "Quantité exacte de tBILL, jusqu'à six décimales. Utiliser un point décimal." : buying ? 'Budget maximal en sEUR. Un reliquat peut rester après arrondi.' : 'Montant minimal de sEUR à recevoir. Le résultat peut être légèrement supérieur après arrondi.';
}
function refreshExpiry() {
  if (!activeRfq) return;
  const remaining = Math.max(0, activeRfq.quote_expires_at - Math.floor(Date.now() / 1000));
  for (const button of quotes.querySelectorAll<HTMLButtonElement>('button')) button.disabled = busy || !!pending || !!trackedTrade || remaining === 0 || activeRfq.status !== 'QUOTED';
  if (!busy) status.textContent = activeRfq.status !== 'QUOTED' ? 'RFQ acceptée.' : remaining ? `RFQ ${activeRfq.rfq_id} — expiration dans ${remaining} s, sans réservation à ce stade.` : 'Quotes expirées. Demander de nouvelles quotes.';
}
setInterval(refreshExpiry, 1000);
form.addEventListener('input', clear);
async function loadConfig() {
  const settings = await api<{ signing_enabled: boolean; settlement_enabled?: boolean; deployment?: Deployment }>('/api/config');
  if (!settings.signing_enabled || !settings.deployment) throw new Error('Signatures désactivées : démarrer Go avec le fichier de déploiement Anvil.');
  config = settings.deployment;
  settlementEnabled = settings.settlement_enabled === true;
  return config;
}
async function reconcileWallet() {
  if (!injected || connecting) return;
  const revision = ++walletRevision;
  try {
    const deployment = config ?? await loadConfig();
    const selected = await readWallet(injected);
    if (revision !== walletRevision || connecting) return;
    const ready = selected.chainId === BigInt(deployment.chainId);
    const nextAccount = ready ? selected.account : '';
    if (account.toLowerCase() !== nextAccount.toLowerCase()) clear();
    account = nextAccount;
    void refreshBalances();
    connect.textContent = !selected.account ? 'Connecter le wallet' : ready ? 'Wallet connecté' : 'Passer sur Anvil';
    walletStatus.textContent = !selected.account ? 'Déverrouiller MetaMask et connecter le compte au site.' : ready ? `${account} — Anvil ${deployment.chainId}` : `Compte autorisé : ${selected.account}. Réseau ${selected.chainId} sélectionné ; Anvil ${deployment.chainId} attendu.`;
  } catch (error) {
    if (revision !== walletRevision || connecting) return;
    account = ''; clear(); walletStatus.textContent = walletError(error);
    connect.textContent = 'Connecter le wallet';
  }
}
for (const event of ['accountsChanged', 'chainChanged']) injected?.on?.(event, () => {
  // Invalider immédiatement une signature en cours, avant toute lecture asynchrone.
  walletRevision++; account = ''; clear();
  if (!connecting) void reconcileWallet();
});
injected?.on?.('disconnect', () => {
  walletRevision++; account = ''; clear();
  connect.textContent = 'Connecter le wallet';
  walletStatus.textContent = 'Wallet déconnecté du réseau. Vérifier Anvil et recharger la page si nécessaire.';
});
void reconcileWallet();
connect.addEventListener('click', async () => {
  if (!injected) { walletStatus.textContent = 'Installer ou ouvrir un wallet EVM injecté dans ce navigateur.'; return; }
  lock(true); connecting = true; walletRevision++;
  try {
    const deployment = await loadConfig();
    account = await connectWallet(injected, deployment.chainId);
    void refreshBalances();
    clear(); connect.textContent = 'Wallet connecté'; walletStatus.textContent = `${account} — Anvil ${deployment.chainId}`;
  } catch (error) { account = ''; clear(); connect.textContent = 'Connecter le wallet'; walletStatus.textContent = walletError(error); }
  finally { connecting = false; lock(false); }
});

async function resume() {
  if (!pending || !injected || !config) return;
  lock(true);
  const saved = pending;
  try {
    await checkWallet(injected, saved.rfq.trader_id, config.chainId);
    tradeStatus.textContent = 'Vérification de la signature, réservation puis signature MM…';
    const result = await api<{ status: string; settlement_submitted: boolean; settlement?: Settlement; typed_data: TypedTrade; buyer_signature: string; seller_signature: string }>(`/api/rfqs/${saved.rfq.rfq_id}/accept`, { quote_id: saved.quote.quote_id, trade_id: saved.quote.trade_id, signature: saved.signature });
    if (result.settlement) {
      if (result.settlement.trade_id !== saved.quote.trade_id) throw new Error('Identifiant de settlement inattendu.');
      if (result.typed_data) {
        if (digest({ ...result.typed_data, types: saved.data.types }) !== digest(saved.data)) throw new Error('Termes retournés différents de ceux signés.');
        verifySignatures(saved.data, result.buyer_signature, result.seller_signature);
      }
      trackedTrade = saved.quote.trade_id; sessionStorage.setItem('settlement-trade', trackedTrade);
      if (activeRfq?.rfq_id === saved.rfq.rfq_id) activeRfq.status = 'ACCEPTED';
      pending = undefined; retry.hidden = true;
      void pollSettlement(); return;
    }
    if (digest({ ...result.typed_data, types: saved.data.types }) !== digest(saved.data)) throw new Error('Termes retournés différents de ceux signés.');
    verifySignatures(saved.data, result.buyer_signature, result.seller_signature);
    if (result.status !== 'SIGNED' || result.settlement_submitted) throw new Error('Statut inattendu.');
    if (activeRfq?.rfq_id === saved.rfq.rfq_id) activeRfq.status = 'ACCEPTED';
    tradeStatus.textContent = `Trade ${saved.quote.trade_id} signé par les deux contreparties, inventaire MM réservé. Aucune transaction soumise. Deadline : ${new Date(saved.rfq.settlement_deadline * 1000).toLocaleTimeString()}.`;
    pending = undefined; retry.hidden = true;
  } catch (error) {
    if (error instanceof APIError && ['INVALID_SIGNATURE', 'QUOTE_EXPIRED', 'INSUFFICIENT_INVENTORY', 'WRONG_TRADE_ID', 'UNKNOWN_RFQ', 'UNKNOWN_QUOTE'].includes(error.code)) {
      pending = undefined; retry.hidden = true; tradeStatus.textContent = `Acceptation refusée : ${error.message}`; return;
    }
    tradeStatus.textContent = `${String(error)} La réservation peut déjà exister. La même signature est conservée en mémoire pour réessayer.`;
    retry.hidden = false;
  } finally { lock(false); }
}
retry.addEventListener('click', () => { void resume(); });

async function accept(rfq: Rfq, quote: Quote) {
  if (!injected || !config || busy || pending || trackedTrade) return;
  const generation = epoch;
  lock(true);
  try {
    await checkWallet(injected, rfq.trader_id, config.chainId);
    const expected = typedTrade(config, rfq, quote);
    const prepared = await api<{ typed_data: TypedTrade }>(`/api/rfqs/${rfq.rfq_id}/quotes/${quote.quote_id}/terms`);
    if (digest({ ...prepared.typed_data, types: expected.types }) !== digest(expected)) throw new Error('Termes serveur différents de la quote affichée.');
    if (generation !== epoch) throw new Error('Wallet ou demande modifié.');
    tradeStatus.textContent = settlementEnabled ? 'Confirmer les termes dans MetaMask. Go soumettra ensuite le settlement sur Anvil.' : 'Confirmer les termes EIP-712 dans le wallet. Submitter Go désactivé.';
    const signer = await new BrowserProvider(injected).getSigner(rfq.trader_id);
    const signature = await signer.signTypedData(expected.domain, expected.types, expected.message);
    await checkWallet(injected, rfq.trader_id, config.chainId);
    if (generation !== epoch) throw new Error('Wallet ou demande modifié pendant la signature.');
    pending = { rfq, quote, data: expected, signature };
  } catch (error) { tradeStatus.textContent = `Acceptation non envoyée : ${String(error)}`; }
  finally { lock(false); }
  if (pending) await resume();
}

form.addEventListener('submit', async event => {
  event.preventDefault(); if (busy) return; clear();
  if (!/^\d+(\.\d{1,6})?$/.test(amount.value) || !/[1-9]/.test(amount.value)) { status.textContent = 'Saisir un montant positif avec au plus six décimales.'; return; }
  const generation = epoch; const buying = side.value === 'BUY';
  lock(true); status.textContent = 'Cotation en cours…';
  try {
    const input = { side: side.value, unit: unit.value, amount: amount.value };
    if (account && config && injected) await checkWallet(injected, account, config.chainId);
    const result = await api<Rfq>(account ? '/api/rfqs' : '/api/quotes', account ? { ...input, trader_id: account } : input);
    if (generation !== epoch) return;
    activeRfq = result.rfq_id ? result : undefined;
    status.textContent = activeRfq ? 'RFQ reçue.' : 'Quotes indicatives reçues, sans réservation. Connecter le wallet pour signer.';
    for (const [index, quote] of result.quotes.entries()) {
      const item = document.createElement('li');
      const flow = buying ? `Vous recevez ${quote.quantity_tbill} tBILL et payez ${quote.cash_amount} sEUR` : `Vous vendez ${quote.quantity_tbill} tBILL et recevez ${quote.cash_amount} sEUR`;
      item.textContent = `${quote.maker_id}${index === 0 ? ' — Meilleure quote' : ''} : ${flow} (prix unitaire ${quote.unit_price} sEUR/tBILL). `;
      if (activeRfq) {
        const rfq = activeRfq;
        const button = document.createElement('button'); button.type = 'button'; button.textContent = settlementEnabled ? 'Signer et exécuter' : 'Signer et réserver';
        button.addEventListener('click', () => { void accept(rfq, quote); }); item.append(button);
      }
      quotes.append(item);
    }
    showRejections(result.rejections);
  } catch (error) { if (generation === epoch) { status.textContent = String(error); if (error instanceof APIError) showRejections(error.rejections); } }
  finally { lock(false); }
});

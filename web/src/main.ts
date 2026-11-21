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
const quotes = document.querySelector<HTMLTableSectionElement>('#quotes')!;
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
    if (account === selected) balances.textContent = `Balances: ${result.sEUR} sEUR · ${result.tBILL} tBILL`;
  } catch { if (account === selected) balances.textContent = 'Anvil balances unavailable.'; }
}
async function pollSettlement() {
  if (!trackedTrade || polling) return;
  polling = true;
  try {
    const result = await api<Settlement>(`/api/settlements/${trackedTrade}`);
    tradeStatus.dataset.state = result.status;
    tradeStatus.textContent = `${result.status} — ${result.message}${result.transaction_hash ? ` Transaction : ${result.transaction_hash}` : ''}`;
    if (result.status === 'SETTLED' || result.status === 'SETTLEMENT_FAILED') {
      void refreshMarket();
      trackedTrade = ''; sessionStorage.removeItem('settlement-trade');
      await refreshBalances(); refreshExpiry();
    }
  } catch (error) { tradeStatus.textContent = `Tracking interrupted: ${String(error)} The trade result still needs verification.`; }
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
  if (!response.ok) throw new APIError(result.message ?? 'Request rejected.', result.code, result.rejections ?? []);
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
  unit.options[1].textContent = buying ? 'A maximum sEUR budget' : 'Minimum sEUR proceeds';
  amountLabel.textContent = unit.value === 'tBILL' ? `tBILL quantity to ${buying ? 'buy' : 'sell'}` : buying ? 'Maximum budget (sEUR)' : 'Minimum proceeds (sEUR)';
  help.textContent = unit.value === 'tBILL' ? "Exact tBILL quantity, up to six decimal places. Use a decimal point." : buying ? 'Maximum sEUR budget. Rounding may leave some cash unspent.' : 'Minimum sEUR proceeds. Rounding may result in slightly higher proceeds.';
}
function refreshExpiry() {
  if (!activeRfq) return;
  const remaining = Math.max(0, activeRfq.quote_expires_at - Math.floor(Date.now() / 1000));
  for (const button of quotes.querySelectorAll<HTMLButtonElement>('button')) button.disabled = busy || !!pending || !!trackedTrade || remaining === 0 || activeRfq.status !== 'QUOTED';
  if (!busy) status.textContent = activeRfq.status !== 'QUOTED' ? 'RFQ accepted.' : remaining ? `RFQ ${activeRfq.rfq_id} — expires in ${remaining} s; no inventory reserved yet.` : 'Quotes expired. Request new quotes.';
}
setInterval(refreshExpiry, 1000);
form.addEventListener('input', clear);
async function loadConfig() {
  const settings = await api<{ signing_enabled: boolean; settlement_enabled?: boolean; deployment?: Deployment }>('/api/config');
  if (!settings.signing_enabled || !settings.deployment) throw new Error('Signing disabled: start Go with the Anvil deployment file.');
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
    connect.textContent = !selected.account ? 'Connect wallet' : ready ? 'Wallet connected' : 'Switch to Anvil';
    walletStatus.textContent = !selected.account ? 'Unlock MetaMask and connect the account to this site.' : ready ? `${account} — Anvil ${deployment.chainId}` : `Authorized account: ${selected.account}. Selected network ${selected.chainId}; expected Anvil ${deployment.chainId}.`;
  } catch (error) {
    if (revision !== walletRevision || connecting) return;
    account = ''; clear(); walletStatus.textContent = walletError(error);
    connect.textContent = 'Connect wallet';
  }
}
for (const event of ['accountsChanged', 'chainChanged']) injected?.on?.(event, () => {
  // Invalidate any pending signing flow before the next asynchronous read.
  walletRevision++; account = ''; clear();
  if (!connecting) void reconcileWallet();
});
injected?.on?.('disconnect', () => {
  walletRevision++; account = ''; clear();
  connect.textContent = 'Connect wallet';
  walletStatus.textContent = 'Wallet disconnected. Check Anvil and reload the page if needed.';
});
void reconcileWallet();
connect.addEventListener('click', async () => {
  if (!injected) { walletStatus.textContent = 'Install or open an injected EVM wallet in this browser.'; return; }
  lock(true); connecting = true; walletRevision++;
  try {
    const deployment = await loadConfig();
    account = await connectWallet(injected, deployment.chainId);
    void refreshBalances();
    clear(); connect.textContent = 'Wallet connected'; walletStatus.textContent = `${account} — Anvil ${deployment.chainId}`;
  } catch (error) { account = ''; clear(); connect.textContent = 'Connect wallet'; walletStatus.textContent = walletError(error); }
  finally { connecting = false; lock(false); }
});

async function resume() {
  if (!pending || !injected || !config) return;
  lock(true);
  const saved = pending;
  try {
    await checkWallet(injected, saved.rfq.trader_id, config.chainId);
    tradeStatus.textContent = 'Verifying signature, reserving inventory and signing for the market maker...';
    const result = await api<{ status: string; settlement_submitted: boolean; settlement?: Settlement; typed_data: TypedTrade; buyer_signature: string; seller_signature: string }>(`/api/rfqs/${saved.rfq.rfq_id}/accept`, { quote_id: saved.quote.quote_id, trade_id: saved.quote.trade_id, signature: saved.signature });
    if (result.settlement) {
      void refreshMarket();
      if (result.settlement.trade_id !== saved.quote.trade_id) throw new Error('Unexpected settlement ID.');
      if (result.typed_data) {
        if (digest({ ...result.typed_data, types: saved.data.types }) !== digest(saved.data)) throw new Error('Returned terms differ from the signed terms.');
        verifySignatures(saved.data, result.buyer_signature, result.seller_signature);
      }
      trackedTrade = saved.quote.trade_id; sessionStorage.setItem('settlement-trade', trackedTrade);
      if (activeRfq?.rfq_id === saved.rfq.rfq_id) activeRfq.status = 'ACCEPTED';
      pending = undefined; retry.hidden = true;
      void pollSettlement(); return;
    }
    if (digest({ ...result.typed_data, types: saved.data.types }) !== digest(saved.data)) throw new Error('Returned terms differ from the signed terms.');
    verifySignatures(saved.data, result.buyer_signature, result.seller_signature);
    if (result.status !== 'SIGNED' || result.settlement_submitted) throw new Error('Unexpected status.');
    if (activeRfq?.rfq_id === saved.rfq.rfq_id) activeRfq.status = 'ACCEPTED';
    tradeStatus.textContent = `Trade ${saved.quote.trade_id} signed by both counterparties; maker inventory reserved. No transaction submitted. Deadline: ${new Date(saved.rfq.settlement_deadline * 1000).toLocaleTimeString()}.`;
    pending = undefined; retry.hidden = true;
  } catch (error) {
    if (error instanceof APIError && ['INVALID_SIGNATURE', 'QUOTE_EXPIRED', 'INSUFFICIENT_INVENTORY', 'WRONG_TRADE_ID', 'UNKNOWN_RFQ', 'UNKNOWN_QUOTE'].includes(error.code)) {
      pending = undefined; retry.hidden = true; tradeStatus.textContent = `Acceptance rejected: ${error.message}`; return;
    }
    tradeStatus.textContent = `${String(error)} The reservation may already exist. The same signature is kept in memory for a retry.`;
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
    if (digest({ ...prepared.typed_data, types: expected.types }) !== digest(expected)) throw new Error('Server terms differ from the displayed quote.');
    if (generation !== epoch) throw new Error('Wallet or request changed.');
    tradeStatus.dataset.state = 'SIGNING';
    tradeStatus.textContent = settlementEnabled ? 'Confirm the terms in MetaMask. The backend will submit settlement on Anvil.' : 'Confirm the EIP-712 terms in your wallet. Settlement submission is disabled.';
    const signer = await new BrowserProvider(injected).getSigner(rfq.trader_id);
    const signature = await signer.signTypedData(expected.domain, expected.types, expected.message);
    await checkWallet(injected, rfq.trader_id, config.chainId);
    if (generation !== epoch) throw new Error('Wallet or request changed during signing.');
    pending = { rfq, quote, data: expected, signature };
  } catch (error) { tradeStatus.textContent = `Acceptance not sent: ${String(error)}`; }
  finally { lock(false); }
  if (pending) await resume();
}

form.addEventListener('submit', async event => {
  event.preventDefault(); if (busy) return; clear();
  if (!/^\d+(\.\d{1,6})?$/.test(amount.value) || !/[1-9]/.test(amount.value)) { status.textContent = 'Enter a positive amount with at most six decimal places.'; return; }
  const generation = epoch; const buying = side.value === 'BUY';
  lock(true); status.textContent = 'Requesting quotes...';
  try {
    const input = { side: side.value, unit: unit.value, amount: amount.value };
    if (account && config && injected) await checkWallet(injected, account, config.chainId);
    const result = await api<Rfq>(account ? '/api/rfqs' : '/api/quotes', account ? { ...input, trader_id: account } : input);
    if (generation !== epoch) return;
    activeRfq = result.rfq_id ? result : undefined;
    status.textContent = activeRfq ? 'RFQ received.' : 'Indicative quotes received; no inventory reserved. Connect your wallet to sign.';
    for (const [index, quote] of result.quotes.entries()) {
      const item = document.createElement('tr');
      item.className = index === 0 ? 'best' : '';
      const flow = buying ? `You receive ${quote.quantity_tbill} tBILL and pay ${quote.cash_amount} sEUR` : `You sell ${quote.quantity_tbill} tBILL and receive ${quote.cash_amount} sEUR`;
      item.title = flow;
      for (const value of [quote.maker_id + (index === 0 ? ' · Best' : ''), quote.unit_price, quote.quantity_tbill, quote.cash_amount]) {
        const cell = document.createElement('td'); cell.textContent = value; item.append(cell);
      }
      const action = document.createElement('td'); item.append(action);
      if (activeRfq) {
        const rfq = activeRfq;
        const button = document.createElement('button'); button.type = 'button'; button.textContent = settlementEnabled ? 'Sign and execute' : 'Sign and reserve';
        button.addEventListener('click', () => { void accept(rfq, quote); }); action.append(button);
      }
      quotes.append(item);
    }
    showRejections(result.rejections);
  } catch (error) { if (generation === epoch) { status.textContent = String(error); if (error instanceof APIError) showRejections(error.rejections); } }
  finally { lock(false); }
});

type Maker = { id: string; bid: string; ask: string; tbill: string; cash: string; reserved_tbill: string; reserved_cash: string; available_tbill: string; available_cash: string };
let marketBusy = false;
async function refreshMarket() {
 if (marketBusy) return; marketBusy = true;
 const message = document.querySelector<HTMLElement>('#market-status')!;
 const container = document.querySelector<HTMLElement>('#makers')!;
 try {
  const market = await api<{reference_price: string; makers: Maker[]}>('/api/market');
  const cards = market.makers.map(m => {
   const card = document.createElement('article'); card.className = 'maker';
   const title = document.createElement('h3'); title.textContent = m.id; card.append(title);
   const list = document.createElement('dl');
   for (const [label, value] of [['Bid / Ask', m.bid + ' / ' + m.ask], ['tBILL inventory', m.tbill], ['Cash sEUR', m.cash], ['Reserved tBILL / sEUR', m.reserved_tbill + ' / ' + m.reserved_cash], ['Available tBILL / sEUR', m.available_tbill + ' / ' + m.available_cash]]) {
    const dt = document.createElement('dt'); dt.textContent = label; const dd = document.createElement('dd'); dd.textContent = value; list.append(dt, dd);
   }
   card.append(list); return card;
  });
  container.replaceChildren(...cards);
  message.textContent = 'Reference: ' + market.reference_price + ' sEUR/tBILL · Updated at ' + new Date().toLocaleTimeString();
 } catch { container.replaceChildren(); message.textContent = 'Market unavailable; retrying automatically.'; }
 finally { marketBusy = false; }
}
void refreshMarket(); setInterval(() => { void refreshMarket(); }, 3000);

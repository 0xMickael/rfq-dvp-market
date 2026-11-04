import assert from 'node:assert/strict';
import { Wallet } from 'ethers';
import { typedTrade, digest, verifySignatures } from '../.test-dist/terms.js';

const base = 'http://127.0.0.1:5173';
const trader = new Wallet('0x' + 'a11ce'.padStart(64, '0'));
async function api(path, body) {
  const response = await fetch(base + path, { method: body === undefined ? 'GET' : 'POST', headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(5000) });
  return { status: response.status, body: await response.json() };
}
const settings = await api('/api/config');
assert.equal(settings.body.signing_enabled, true);
const config = settings.body.deployment;
async function create(side, unit, amount) {
  const r = await api('/api/rfqs', { trader_id: trader.address, side, unit, amount });
  assert.equal(r.status, 201, JSON.stringify(r.body)); return r.body;
}
async function prepare(rfq) {
  const quote = rfq.quotes[0];
  const data = typedTrade(config, rfq, quote);
  const prepared = await api(`/api/rfqs/${rfq.rfq_id}/quotes/${quote.quote_id}/terms`);
  assert.equal(prepared.status, 200);
  assert.equal(digest({ ...prepared.body.typed_data, types: data.types }), digest(data));
  return { data, payload: { quote_id: quote.quote_id, trade_id: quote.trade_id, signature: await trader.signTypedData(data.domain, data.types, data.message) } };
}
function verify(result, data) {
  assert.equal(result.status, 200, JSON.stringify(result.body));
  assert.equal(result.body.status, 'SIGNED');
  assert.equal(result.body.settlement_submitted, false);
  verifySignatures(data, result.body.buyer_signature, result.body.seller_signature);
}
for (const side of ['BUY', 'SELL']) {
  for (const unit of ['tBILL', 'sEUR']) {
    const rfq = await create(side, unit, '100.123456');
    const { data, payload } = await prepare(rfq);
    const bad = await api(`/api/rfqs/${rfq.rfq_id}/accept`, { ...payload, signature: '0x00' });
    assert.equal(bad.status, 403); assert.equal(bad.body.code, 'INVALID_SIGNATURE');
    assert.equal((await api(`/api/rfqs/${rfq.rfq_id}`)).body.status, 'QUOTED');
    const accepted = await api(`/api/rfqs/${rfq.rfq_id}/accept`, payload);
    verify(accepted, data);
    assert.equal(accepted.body.trade.reserved_asset, side === 'BUY' ? 'tBILL' : 'sEUR');
    const again = await api(`/api/rfqs/${rfq.rfq_id}/accept`, payload);
    assert.deepEqual(again, accepted);
    const stored = await api(`/api/rfqs/${rfq.rfq_id}`);
    assert.equal(stored.body.status, 'ACCEPTED');
    assert.equal(stored.body.trade.trade_id, payload.trade_id);
  }
}
for (const side of ['BUY', 'SELL']) {
  const rfqs = await Promise.all([create(side, 'tBILL', '700'), create(side, 'tBILL', '700')]);
  assert.equal(rfqs[0].quotes[0].maker_id, rfqs[1].quotes[0].maker_id);
  const prepared = await Promise.all(rfqs.map(prepare));
  const results = await Promise.all(rfqs.map((rfq, i) => api(`/api/rfqs/${rfq.rfq_id}/accept`, prepared[i].payload)));
  assert.equal(results.filter(r => r.status === 200).length, 1);
  assert.equal(results.filter(r => r.status === 409 && r.body.code === 'INSUFFICIENT_INVENTORY').length, 1);
  for (const [i, result] of results.entries()) if (result.status === 200) verify(result, prepared[i].data);
}
console.log('OK : Vite -> Go -> Rust, quatre modes signés, signatures ethers/Go, refus avant réservation, retry et concurrence BUY/SELL. Aucune transaction soumise.');

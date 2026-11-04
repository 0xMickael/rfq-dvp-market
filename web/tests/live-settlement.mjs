import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Contract, HDNodeWallet, JsonRpcProvider, parseUnits } from 'ethers';
import { typedTrade, verifySignatures } from '../.test-dist/terms.js';
const provider = new JsonRpcProvider('http://127.0.0.1:18545', 31337, { cacheTimeout: -1 });
const trader = HDNodeWallet.fromPhrase('test test test test test test test test test test test junk', undefined, "m/44'/60'/0'/0/1").connect(provider);
const base = 'http://127.0.0.1:18080';
async function api(path, body) {
 const r = await fetch(base + path, { method: body === undefined ? 'GET' : 'POST', headers: {'Content-Type':'application/json'}, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(5000) });
 const data = await r.json(); assert.ok(r.ok, JSON.stringify(data)); return data;
}
const settings = await api('/api/config'); assert.equal(settings.settlement_enabled, true);
const c = settings.deployment;
const erc20 = ['function balanceOf(address) view returns(uint256)', 'function approve(address,uint256) returns(bool)'];
const cash = new Contract(c.cashToken, erc20, trader), security = new Contract(c.securityToken, erc20, trader);
const artifact = JSON.parse(await readFile(new URL('../../contracts/out/DvPSettlement.sol/DvPSettlement.json', import.meta.url)));
const dvp = new Contract(c.settlement, artifact.abi, provider);
async function prepare(side, unit = 'tBILL', amount = '1.123456') {
 const rfq = await api('/api/rfqs', {trader_id:trader.address, side, unit, amount});
 const q = rfq.quotes[0], data = typedTrade(c, rfq, q);
 const signature = await trader.signTypedData(data.domain, data.types, data.message);
 return {rfq, q, data, payload:{quote_id:q.quote_id, trade_id:q.trade_id, signature}};
}
async function wait(p, wanted = 'SETTLED') {
 for (let i = 0; i < 80; i++) {
  const result = await api(`/api/settlements/${p.q.trade_id}`);
  if (result.status === wanted) return result;
  if (i === 79) assert.fail(JSON.stringify(result));
  await new Promise(r => setTimeout(r, 250));
 }
}
for (const side of ['BUY','SELL']) for (const unit of ['tBILL','sEUR']) {
 const p = await prepare(side, unit, '10.123456');
 const maker = c[p.q.maker_id.toLowerCase()];
 const before = await Promise.all([cash.balanceOf(trader.address),security.balanceOf(trader.address),cash.balanceOf(maker),security.balanceOf(maker)]);
 const signed = await api(`/api/rfqs/${p.rfq.rfq_id}/accept`, p.payload);
 verifySignatures(p.data, signed.buyer_signature, signed.seller_signature);
 const result = await wait(p); assert.match(result.transaction_hash, /^0x[0-9a-f]{64}$/i);
 const receipt = await provider.getTransactionReceipt(result.transaction_hash); assert.equal(receipt.status,1);
 assert.equal(await dvp.settled(p.data.message.buyer,p.data.message.seller,p.q.trade_id),true);
 const after = await Promise.all([cash.balanceOf(trader.address),security.balanceOf(trader.address),cash.balanceOf(maker),security.balanceOf(maker)]);
 const direction = side === 'BUY' ? 1n : -1n, money = parseUnits(p.q.cash_amount,6), qty = parseUnits(p.q.quantity_tbill,6);
 assert.deepEqual(after.map((n,i)=>n-before[i]),[-direction*money,direction*qty,direction*money,-direction*qty]);
 const repeat = await api(`/api/rfqs/${p.rfq.rfq_id}/accept`, p.payload);
 assert.equal(repeat.settlement.transaction_hash,result.transaction_hash);
 assert.equal((await api(`/api/rfqs/${p.rfq.rfq_id}`)).trade.status,'SETTLED');
 console.log(`OK ${side}/${unit}: ${result.transaction_hash}, soldes exacts, retry sans double règlement`);
}
// Plusieurs transactions du même submitter : nonces distincts et aucun doublon.
const concurrent = await Promise.all([prepare('BUY'),prepare('SELL')]);
await Promise.all(concurrent.map(p=>api(`/api/rfqs/${p.rfq.rfq_id}/accept`,p.payload)));
const results = await Promise.all(concurrent.map(p=>wait(p)));
assert.notEqual(results[0].transaction_hash,results[1].transaction_hash);
const txs = await Promise.all(results.map(r=>provider.getTransaction(r.transaction_hash)));
assert.notEqual(txs[0].nonce,txs[1].nonce);
console.log('OK concurrence BUY/SELL : nonces distincts');
// Une allowance manquante ne libère pas prématurément la réservation.
await (await cash.approve(c.settlement,0)).wait();
const failed = await prepare('BUY');
await api(`/api/rfqs/${failed.rfq.rfq_id}/accept`,failed.payload);
await new Promise(r=>setTimeout(r,2500));
assert.equal((await api(`/api/settlements/${failed.q.trade_id}`)).status,'SETTLEMENT_PENDING');
assert.equal((await api(`/api/rfqs/${failed.rfq.rfq_id}`)).trade.status,'SETTLEMENT_PENDING');
await provider.send('evm_increaseTime',[601]); await provider.send('evm_mine',[]);
await wait(failed,'SETTLEMENT_FAILED');
assert.equal(await dvp.settled(failed.data.message.buyer,failed.data.message.seller,failed.q.trade_id),false);
assert.equal((await api(`/api/rfqs/${failed.rfq.rfq_id}`)).trade.status,'SETTLEMENT_FAILED');
console.log('OK allowance absente : conservation puis libération après deadline on-chain');
provider.destroy();

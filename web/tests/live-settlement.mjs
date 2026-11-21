import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Contract, HDNodeWallet, JsonRpcProvider, parseUnits } from 'ethers';
import { typedTrade, verifySignatures } from '../.test-dist/terms.js';
const provider = new JsonRpcProvider('http://127.0.0.1:18545', 31337, { cacheTimeout: -1 });
const trader = HDNodeWallet.fromPhrase('test test test test test test test test test test test junk', undefined, "m/44'/60'/0'/0/1").connect(provider);
const base = process.env.TEST_API_URL ?? 'http://127.0.0.1:18080';
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
async function market() {
 const snapshot = await api('/api/market');
 assert.equal(snapshot.makers.length, 3);
 for (const m of snapshot.makers) {
  for (const asset of ['tbill', 'cash']) {
   const total = parseUnits(m[asset],6), reserved = parseUnits(m[`reserved_${asset}`],6), available = parseUnits(m[`available_${asset}`],6);
   assert.ok(available >= 0n && reserved >= 0n);
   assert.equal(total, reserved + available);
  }
 }
 return snapshot;
}
// Indicative quotes also pass through the real Vite -> Go -> Rust stack.
for (const side of ['BUY','SELL']) for (const unit of ['tBILL','sEUR']) {
 const book = await api('/api/quotes', {side,unit,amount:'1.123456'});
 assert.equal(book.quotes.length,3);
 assert.equal(book.quotes[0].maker_id,side === 'BUY' ? 'MM2' : 'MM3');
}
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
 const stockBefore = (await market()).makers.find(m=>m.id === p.q.maker_id);
 const before = await Promise.all([cash.balanceOf(trader.address),security.balanceOf(trader.address),cash.balanceOf(maker),security.balanceOf(maker)]);
 const signed = await api(`/api/rfqs/${p.rfq.rfq_id}/accept`, p.payload);
 verifySignatures(p.data, signed.buyer_signature, signed.seller_signature);
 const result = await wait(p); assert.match(result.transaction_hash, /^0x[0-9a-f]{64}$/i);
 const receipt = await provider.getTransactionReceipt(result.transaction_hash); assert.equal(receipt.status,1);
 assert.equal(await dvp.settled(p.data.message.buyer,p.data.message.seller,p.q.trade_id),true);
 const after = await Promise.all([cash.balanceOf(trader.address),security.balanceOf(trader.address),cash.balanceOf(maker),security.balanceOf(maker)]);
 const direction = side === 'BUY' ? 1n : -1n, money = parseUnits(p.q.cash_amount,6), qty = parseUnits(p.q.quantity_tbill,6);
 assert.deepEqual(after.map((n,i)=>n-before[i]),[-direction*money,direction*qty,direction*money,-direction*qty]);
 const stockAfter = (await market()).makers.find(m=>m.id === p.q.maker_id);
 assert.equal(parseUnits(stockAfter.cash,6)-parseUnits(stockBefore.cash,6),direction*money);
 assert.equal(parseUnits(stockAfter.tbill,6)-parseUnits(stockBefore.tbill,6),-direction*qty);
 assert.equal(stockAfter.reserved_cash,'0.000000');
 assert.equal(stockAfter.reserved_tbill,'0.000000');
 const repeat = await api(`/api/rfqs/${p.rfq.rfq_id}/accept`, p.payload);
 assert.equal(repeat.settlement.transaction_hash,result.transaction_hash);
 assert.equal((await api(`/api/rfqs/${p.rfq.rfq_id}`)).trade.status,'SETTLED');
 // Repeat an actual internal notification: no second accounting entry.
 const snapshot = await market();
 const block = await provider.getBlock(receipt.blockNumber);
 const notification = await fetch(`http://127.0.0.1:18081/rfqs/${p.rfq.rfq_id}/settlement`, {
  method:'POST', headers:{'Content-Type':'application/json'}, signal:AbortSignal.timeout(5000),
  body:JSON.stringify({trade_id:p.q.trade_id,status:'SETTLED',transaction_hash:result.transaction_hash,block_timestamp:block.timestamp}),
 });
 assert.equal(notification.status,200);
 assert.deepEqual(await market(),snapshot);
 console.log(`OK ${side}/${unit}: ${result.transaction_hash}, exact balances, retry without duplicate settlement`);
}
// Concurrent transactions from one submitter use distinct nonces without duplicates.
const concurrent = await Promise.all([prepare('BUY'),prepare('SELL')]);
await Promise.all(concurrent.map(p=>api(`/api/rfqs/${p.rfq.rfq_id}/accept`,p.payload)));
const results = await Promise.all(concurrent.map(p=>wait(p)));
assert.notEqual(results[0].transaction_hash,results[1].transaction_hash);
const txs = await Promise.all(results.map(r=>provider.getTransaction(r.transaction_hash)));
assert.notEqual(txs[0].nonce,txs[1].nonce);
console.log('OK concurrent BUY/SELL: distinct nonces');
// A missing allowance must not release a reservation early.
await (await cash.approve(c.settlement,0)).wait();
const failed = await prepare('BUY');
const beforeFailure = await market();
await api(`/api/rfqs/${failed.rfq.rfq_id}/accept`,failed.payload);
await new Promise(r=>setTimeout(r,2500));
assert.equal((await api(`/api/settlements/${failed.q.trade_id}`)).status,'SETTLEMENT_PENDING');
assert.equal((await api(`/api/rfqs/${failed.rfq.rfq_id}`)).trade.status,'SETTLEMENT_PENDING');
const reservedMaker = (await market()).makers.find(m=>m.id === failed.q.maker_id);
assert.equal(reservedMaker.reserved_tbill,failed.q.quantity_tbill);
assert.equal(reservedMaker.reserved_cash,'0.000000');
await provider.send('evm_increaseTime',[601]); await provider.send('evm_mine',[]);
await wait(failed,'SETTLEMENT_FAILED');
assert.equal(await dvp.settled(failed.data.message.buyer,failed.data.message.seller,failed.q.trade_id),false);
assert.equal((await api(`/api/rfqs/${failed.rfq.rfq_id}`)).trade.status,'SETTLEMENT_FAILED');
assert.deepEqual(await market(),beforeFailure);
console.log('OK missing allowance: reservation retained, then released after the on-chain deadline');
provider.destroy();

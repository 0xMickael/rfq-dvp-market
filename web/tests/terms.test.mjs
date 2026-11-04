import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Wallet, TypedDataEncoder } from 'ethers';
import { typedTrade, tradeTypes, digest, verifySignatures } from '../.test-dist/terms.js';
import { checkWallet, connectWallet, readWallet, walletError } from '../.test-dist/wallet.js';
const fixture = JSON.parse(await readFile(new URL('../../contracts/fixtures/buy-100.typed-data.json', import.meta.url)));
const signatures = JSON.parse(await readFile(new URL('../../contracts/fixtures/buy-100.signatures.json', import.meta.url)));
const config = { chainId: 31337, settlement: fixture.domain.verifyingContract, cashToken: fixture.message.cashToken, securityToken: fixture.message.securityToken, mm2: fixture.message.seller };
const rfq = { trader_id: fixture.message.buyer, quote_expires_at: 1999999900, settlement_deadline: 2000000000 };
const quote = { maker_id: 'MM2', trade_id: fixture.message.tradeId, side: 'BUY', quantity_tbill: '100.000000', cash_amount: '9804.921500' };

test('la construction TypeScript reproduit la fixture Solidity/Cast', () => {
  const data = typedTrade(config, rfq, quote);
  assert.equal(digest(data), TypedDataEncoder.hash(fixture.domain, tradeTypes, fixture.message));
  verifySignatures(data, signatures.buyerSignature, signatures.sellerSignature);
  assert.throws(() => verifySignatures({ ...data, message: { ...data.message, cashAmount: '1' } }, signatures.buyerSignature, signatures.sellerSignature));
});
test('SELL inverse les contreparties et garde les entiers au-delà de Number.MAX_SAFE_INTEGER', async () => {
  const data = typedTrade(config, rfq, { ...quote, side: 'SELL', cash_amount: '9007199254.740993' });
  assert.equal(data.message.seller, fixture.message.buyer);
  assert.equal(data.message.buyer, fixture.message.seller);
  assert.equal(data.message.cashAmount, '9007199254740993');
  const trader = new Wallet('0x' + 'a11ce'.padStart(64, '0'));
  const maker = new Wallet('0x' + 'b0b'.padStart(64, '0'));
  verifySignatures(data, await maker.signTypedData(data.domain, data.types, data.message), await trader.signTypedData(data.domain, data.types, data.message));
});
test('refus des domaines, montants et identifiants invalides', () => {
  for (const cash of ['0.000000', '-1.000000', '1.0000001', '18446744073709.551616']) assert.throws(() => typedTrade(config, rfq, { ...quote, cash_amount: cash }));
  assert.throws(() => typedTrade({ ...config, chainId: 1 }, rfq, quote));
  assert.throws(() => typedTrade(config, rfq, { ...quote, trade_id: '0x' + '0'.repeat(64) }));
});
test('changement de compte, de chaîne et déconnexion invalident le wallet', async () => {
  const provider = (accounts, chain) => ({ request: async ({ method }) => method === 'eth_accounts' ? accounts : chain });
  await checkWallet(provider([rfq.trader_id], '0x7a69'), rfq.trader_id, 31337);
  for (const p of [provider([], '0x7a69'), provider([config.mm2], '0x7a69'), provider([rfq.trader_id], '0x1')]) await assert.rejects(checkWallet(p, rfq.trader_id, 31337));
});

function mockWallet({ chain = '0x1', unknown = false, rejection } = {}) {
  const calls = [];
  let account = rfq.trader_id;
  return { calls, request: async ({ method, params }) => {
    calls.push({ method, params });
    if (method === 'eth_accounts') return [account];
    if (method === 'eth_chainId') return chain;
    if (method === 'eth_requestAccounts') {
      if (rejection) throw rejection;
      return [account];
    }
    if (method === 'wallet_switchEthereumChain') {
      if (unknown) throw { code: 4902 };
      chain = params[0].chainId;
      account = config.mm2;
      return null;
    }
    if (method === 'wallet_addEthereumChain') { unknown = false; return null; }
    throw new Error(`Unexpected method ${method}`);
  } };
}

test('lecture silencieuse du compte déjà autorisé, sans popup', async () => {
  const provider = mockWallet({ chain: '0x7a69' });
  assert.deepEqual(await readWallet(provider), { account: rfq.trader_id, chainId: 31337n });
  assert.deepEqual(provider.calls.map(call => call.method), ['eth_accounts', 'eth_chainId']);
  assert.equal(await connectWallet(provider, 31337), rfq.trader_id);
  assert.equal(provider.calls.some(call => call.method.startsWith('wallet_')), false);
});

test('connexion sur un autre réseau : switch Anvil et relecture du compte final', async () => {
  const provider = mockWallet();
  assert.equal(await connectWallet(provider, 31337), config.mm2);
  assert.deepEqual(provider.calls.find(call => call.method === 'wallet_switchEthereumChain').params, [{ chainId: '0x7a69' }]);
});

test('réseau inconnu : ajout du RPC local puis switch explicite', async () => {
  const provider = mockWallet({ unknown: true });
  await connectWallet(provider, 31337);
  const network = provider.calls.find(call => call.method === 'wallet_addEthereumChain').params[0];
  assert.equal(network.chainId, '0x7a69');
  assert.deepEqual(network.rpcUrls, ['http://127.0.0.1:8545']);
  assert.equal(provider.calls.filter(call => call.method === 'wallet_switchEthereumChain').length, 2);
});

test('refus utilisateur et demande déjà ouverte donnent une erreur compréhensible', async () => {
  for (const code of [4001, -32002]) {
    const provider = mockWallet({ rejection: { code } });
    await assert.rejects(connectWallet(provider, 31337), error => error.code === code);
    assert.equal(provider.calls.length, 1);
  }
  assert.match(walletError({ code: 4001 }), /refusée/);
  assert.match(walletError({ code: -32002 }), /déjà ouverte/);
  assert.match(walletError({ code: 4900 }), /Anvil/);
});

test('un switch sans changement effectif du réseau ne valide pas la connexion', async () => {
  const provider = { request: async ({ method }) => method === 'eth_chainId' ? '0x1' : method === 'wallet_switchEthereumChain' ? null : [rfq.trader_id] };
  await assert.rejects(connectWallet(provider, 31337), /Réseau 1 sélectionné/);
});

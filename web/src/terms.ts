import { getAddress, parseUnits, TypedDataEncoder, verifyTypedData } from 'ethers';
export type Quote = { quote_id: string; trade_id: string; maker_id: string; side: string; unit_price: string; quantity_tbill: string; cash_amount: string };
export type Rfq = { rfq_id: string; trader_id: string; status: string; quote_expires_at: number; settlement_deadline: number; quotes: Quote[]; rejections: { maker_id: string; message: string }[] };
export type Deployment = { chainId: number; settlement: string; cashToken: string; securityToken: string; mm1: string; mm2: string; mm3: string };
export const tradeTypes = { Trade: [
  { name: 'tradeId', type: 'bytes32' }, { name: 'buyer', type: 'address' }, { name: 'seller', type: 'address' },
  { name: 'cashToken', type: 'address' }, { name: 'securityToken', type: 'address' },
  { name: 'cashAmount', type: 'uint256' }, { name: 'securityAmount', type: 'uint256' }, { name: 'deadline', type: 'uint256' },
] };
export function typedTrade(config: Deployment, rfq: Rfq, quote: Quote) {
  if (config.chainId !== 31337 || !Number.isSafeInteger(rfq.settlement_deadline) || rfq.settlement_deadline <= rfq.quote_expires_at) throw new Error('Invalid network or deadlines.');
  const makers: Record<string, string> = { MM1: config.mm1, MM2: config.mm2, MM3: config.mm3 };
  const maker = getAddress(makers[quote.maker_id]); const trader = getAddress(rfq.trader_id);
  if (maker === trader || !['BUY', 'SELL'].includes(quote.side)) throw new Error('Invalid counterparties.');
  const exact = (s: string) => {
    if (!/^\d+\.\d{6}$/.test(s)) throw new Error('Invalid amount.');
    const value = parseUnits(s, 6);
    if (value <= 0n || value > 0xffffffffffffffffn) throw new Error('Amount out of range.');
    return value.toString();
  };
  if (!/^0x[\da-f]{64}$/i.test(quote.trade_id) || BigInt(quote.trade_id) === 0n) throw new Error('Invalid trade ID.');
  return {
    domain: { name: 'rfq-dvp-market', version: '1', chainId: config.chainId, verifyingContract: getAddress(config.settlement) }, types: tradeTypes,
    message: { tradeId: quote.trade_id, buyer: quote.side === 'BUY' ? trader : maker, seller: quote.side === 'BUY' ? maker : trader,
      cashToken: getAddress(config.cashToken), securityToken: getAddress(config.securityToken),
      cashAmount: exact(quote.cash_amount), securityAmount: exact(quote.quantity_tbill), deadline: String(rfq.settlement_deadline) },
  };
}
export type TypedTrade = ReturnType<typeof typedTrade>;
export function digest(data: TypedTrade): string { return TypedDataEncoder.hash(data.domain, data.types, data.message); }
export function verifySignatures(data: TypedTrade, buyer: string, seller: string): void {
  if (getAddress(verifyTypedData(data.domain, data.types, data.message, buyer)) !== getAddress(data.message.buyer)
    || getAddress(verifyTypedData(data.domain, data.types, data.message, seller)) !== getAddress(data.message.seller)) throw new Error('Invalid counterparty signatures.');
}

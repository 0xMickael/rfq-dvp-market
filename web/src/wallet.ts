import type { Eip1193Provider } from 'ethers';
export async function readWallet(provider: Eip1193Provider) {
  const accounts: string[] = await provider.request({ method: 'eth_accounts' });
  const chain: string = await provider.request({ method: 'eth_chainId' });
  return { account: accounts[0] ?? '', chainId: BigInt(chain) };
}

export function walletError(error: unknown): string {
  const code = (error as { code?: number } | null)?.code;
  if (code === 4001) return 'Request rejected in the wallet. Click again to retry.';
  if (code === -32002) return 'A request is already open in MetaMask. Open the extension to complete it.';
  if (code === 4900 || code === 4901) return 'Wallet disconnected. Check that Anvil is running at http://127.0.0.1:8545.';
  return error instanceof Error ? error.message : String(error);
}

export async function connectWallet(provider: Eip1193Provider, chainId: number): Promise<string> {
  await provider.request({ method: 'eth_requestAccounts' });
  const current = await readWallet(provider);
  if (current.chainId !== BigInt(chainId)) {
    const chain = { chainId: `0x${chainId.toString(16)}` };
    try {
      await provider.request({ method: 'wallet_switchEthereumChain', params: [chain] });
    } catch (error) {
      if ((error as { code?: number })?.code !== 4902 || chainId !== 31337) throw error;
      await provider.request({ method: 'wallet_addEthereumChain', params: [{ ...chain, chainName: 'Anvil local', nativeCurrency: { name: 'Ether', symbol: 'ETH', decimals: 18 }, rpcUrls: ['http://127.0.0.1:8545'] }] });
      await provider.request({ method: 'wallet_switchEthereumChain', params: [chain] });
    }
  }
  const selected = await readWallet(provider);
  await checkWallet(provider, selected.account, chainId);
  return selected.account;
}

export async function checkWallet(provider: Eip1193Provider, account: string, chainId: number): Promise<void> {
  const selected = await readWallet(provider);
  if (!selected.account) throw new Error('No authorized account. Unlock MetaMask and connect the account to this site.');
  if (selected.account.toLowerCase() !== account.toLowerCase()) throw new Error('The selected account changed. Switch back to the account that requested this quote.');
  if (selected.chainId !== BigInt(chainId)) throw new Error(`Selected network ${selected.chainId}; expected Anvil ${chainId}. Click Switch to Anvil.`);
}

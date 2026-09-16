type Quote = { maker_id: string; unit_price: string; quantity_tbill: string; cash_amount: string };
type Result = { quotes?: Quote[]; rejections: { maker_id: string; message: string }[]; message?: string };
const form = document.querySelector<HTMLFormElement>('#quote-form')!;
const side = document.querySelector<HTMLSelectElement>('#side')!;
const unit = document.querySelector<HTMLSelectElement>('#unit')!;
const amount = document.querySelector<HTMLInputElement>('#amount')!;
const status = document.querySelector<HTMLParagraphElement>('#frontend-status')!;
const quotes = document.querySelector<HTMLUListElement>('#quotes')!;
const rejections = document.querySelector<HTMLUListElement>('#rejections')!;
const help = document.querySelector<HTMLParagraphElement>('#amount-help')!;
const amountLabel = document.querySelector<HTMLSpanElement>('#amount-label')!;
const button = form.querySelector<HTMLButtonElement>('button')!;
let active: AbortController | undefined;
function clear() {
  active?.abort(); active = undefined; button.disabled = false;
  quotes.replaceChildren(); rejections.replaceChildren(); status.textContent = '';
  const buying = side.value === 'BUY';
  unit.options[1].textContent = buying ? 'Un budget maximal en sEUR' : 'Un montant minimum de sEUR à recevoir';
  amountLabel.textContent = unit.value === 'tBILL'
    ? `Quantité de tBILL à ${buying ? 'acheter' : 'vendre'}`
    : buying ? 'Budget maximal à dépenser (sEUR)' : 'Cash minimum à recevoir (sEUR)';
  help.textContent = unit.value === 'tBILL'
    ? "Quantité exacte de tBILL, jusqu'à six décimales. Utiliser un point décimal."
    : side.value === 'BUY' ? 'Budget maximal en sEUR. Un reliquat peut rester après arrondi.'
    : 'Montant minimal de sEUR à recevoir. Le résultat peut être légèrement supérieur après arrondi.';
}
form.addEventListener('input', clear);
form.addEventListener('submit', async (event) => {
  event.preventDefault(); clear();
  if (!/^\d+(\.\d{1,6})?$/.test(amount.value) || !/[1-9]/.test(amount.value)) {
    status.textContent = 'Saisir un montant positif avec au plus six décimales et un point décimal.'; return;
  }
  const controller = new AbortController(); active = controller; button.disabled = true;
  status.textContent = 'Cotation en cours…';
  const timer = setTimeout(() => controller.abort(), 5000);
  try {
    const response = await fetch('/api/quotes', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ side: side.value, unit: unit.value, amount: amount.value }), signal: controller.signal,
    });
    const result: Result = await response.json();
    if (active !== controller) return;
    if (!Array.isArray(result.rejections) || (response.ok && !Array.isArray(result.quotes))) throw new Error('Réponse invalide');
    status.textContent = response.ok ? 'Quotes indicatives reçues, sans réservation.' : result.message ?? 'Demande refusée.';
    for (const [index, quote] of (result.quotes ?? []).entries()) {
      const item = document.createElement('li');
      const flow = side.value === 'BUY'
        ? `Vous recevez ${quote.quantity_tbill} tBILL et payez ${quote.cash_amount} sEUR`
        : `Vous vendez ${quote.quantity_tbill} tBILL et recevez ${quote.cash_amount} sEUR`;
      item.textContent = `${quote.maker_id}${index === 0 ? ' — Meilleure quote' : ''} : ${flow} (prix unitaire ${quote.unit_price} sEUR/tBILL).`;
      quotes.append(item);
    }
    for (const rejection of result.rejections) {
      const item = document.createElement('li'); item.textContent = `${rejection.maker_id} : ${rejection.message}`; rejections.append(item);
    }
  } catch {
    if (active === controller) status.textContent = 'Cotation indisponible ou délai dépassé. Vérifier les services puis réessayer.';
  } finally {
    clearTimeout(timer);
    if (active === controller) { active = undefined; button.disabled = false; }
  }
});

export {};

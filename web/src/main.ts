const status = document.querySelector<HTMLParagraphElement>("#frontend-status");

if (status) {
  status.textContent = "Frontend TypeScript chargé. Aucun appel aux services n'est effectué.";
}

export {};

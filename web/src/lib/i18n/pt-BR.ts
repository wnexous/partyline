import type { Dictionary } from "./en";

/** Brazilian Portuguese strings (primary audience). Same keys as en.ts. */
export const ptBR: Dictionary = {
  "app.name": "partyline",

  "common.loading": "Carregando…",
  "common.retry": "Tentar de novo",
  "common.cancel": "Cancelar",
  "common.save": "Salvar",
  "common.copy": "Copiar",
  "common.copied": "Copiado",
  "common.close": "Fechar",

  "validation.required": "Obrigatório",
  "validation.username": "De 3 a 32 caracteres: letras minúsculas, números, _ ou -",
  "validation.passwordMin": "Pelo menos {min} caracteres",
  "validation.tooLong": "No máximo {max} caracteres",

  "error.network": "Não foi possível falar com o servidor. Verifique sua conexão e tente de novo.",
  "error.generic": "Algo deu errado. Tente de novo.",
};

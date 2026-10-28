/** Lower case without accents, so "joao" finds "João" and "ACAO" finds "ação". */
export function foldText(text: string): string {
  return text.normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();
}

/** Whether every word of `query` appears somewhere in `haystack` (case and accents ignored); a blank query matches. */
export function matchesTerms(haystack: string, query: string): boolean {
  const terms = foldText(query).split(/\s+/).filter(Boolean);
  if (terms.length === 0) return true;
  const folded = foldText(haystack);
  return terms.every((term) => folded.includes(term));
}

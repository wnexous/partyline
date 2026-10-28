/**
 * Puts `text` on the clipboard and says whether it got there (ACT-05). The Clipboard API exists only
 * on a secure origin; an instance served over plain http (a LAN address) falls back to selecting a
 * hidden box and copying it, which every browser still allows from a click.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Refused (a permissions policy, a page that is not focused): the old way may still work.
  }
  const box = document.createElement("textarea");
  box.value = text;
  box.setAttribute("readonly", "");
  box.setAttribute("aria-hidden", "true");
  box.style.cssText = "position:fixed;top:0;left:0;opacity:0;pointer-events:none";
  document.body.append(box);
  try {
    box.select();
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    box.remove();
  }
}

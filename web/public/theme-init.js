// Applies the stored color scheme before first paint (external file: the server CSP forbids
// inline scripts). Keep the storage key in sync with src/lib/theme.ts.
(() => {
  try {
    const stored = localStorage.getItem("partyline.theme");
    const dark =
      stored === "dark" ||
      (stored !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.classList.toggle("dark", dark);
  } catch {
    // Storage blocked: fall back to the stylesheet default.
  }
})();

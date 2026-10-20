---
version: 1
slug: "web-src-routes-root-tsx"
primary_target: "web/src/routes/__root.tsx"
related_targets: []
---

Scope: the whole partyline web app shell (sidebar, live banner, auth pages, and every page later tasks add). Mode: operate.
Audience/job: developers and small teams watching Claude Code agents and chat bridges live while they work elsewhere; pt-BR first.
Constraints: shadcn/ui (Radix) standard controls, light + dark, 360 px up, i18n en/pt-BR. Inferred brief; no interview round was available (batch worker).

## Direction contract

THESIS: The room is a wire: every screen reads like the operator's copy of a telegraph line, where live traffic arrives as freshly pasted tape. Refuses the grey SaaS dashboard with one blue accent and the dark neon "terminal" costume.

OWN-WORLD: Paper ground with a buff second layer for the sidebar/rail; blue-black ink for text and primary actions; telegram-tape yellow as the live/selection colour (in dark mode the ground is ink navy and tape yellow becomes the primary). Archivo (variable width) for UI, with condensed uppercase only for small section labels; JetBrains Mono strictly for data: sequence numbers, times, codes, tokens, commands. Hairline rules, 6px radius, soft offset shadows.

STORY: The visitor sees at a glance which wires are live, who is on them, and whether the line is connected; they read, post, and route with standard controls.

FIRST VIEWPORT: Left buff sidebar (wordmark, nav, live-line status at the bottom with a tape-yellow pulse when connected), content column on paper with a 56px header (page title + primary action at right). Auth pages: centred form card on paper with the wordmark and a tape strip carrying the page's one-line purpose.

FORM: Telegraph/telex wire office (tape strips, counters, form ruling), position 3 of 7 on the ordered list; seed key bd4449a0. Signature move: new live items land with a tape-yellow strip highlight that settles into the paper within ~1.2 s; the connection state is the "line" indicator.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

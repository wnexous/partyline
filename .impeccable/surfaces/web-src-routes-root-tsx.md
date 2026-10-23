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

THESIS: The room is a wire: every screen reads like the operator's copy of a telegraph line, where live traffic arrives as freshly pasted tape. Refuses the generic SaaS dashboard (one bright blue accent, a card grid) and the dark neon "terminal" costume. The owner's neutral brief (2026-10-23) took hue out and his second brief (same day) asked for a little back, so the identity rests on form and the colour stays sparse: one dusty-indigo ink, a quiet green, the muted red and soft faces.

OWN-WORLD: Paper ground with a grey second layer for the sidebar/rail; black ink for text; a dusty-indigo stamp ink for primary actions, links and focus; tape, tinted by that ink, as the live/selection mark (in dark mode the ground is near-black and the accent lifts to a soft periwinkle). Surfaces and ink are greys; colour is the indigo, a quiet green for online, a muted red for errors and destructive actions, and soft warm (people) and cool (AI) tints for faces (see DESIGN.md). Archivo (variable width) for UI, with condensed uppercase only for small section labels; JetBrains Mono strictly for data: sequence numbers, times, codes, tokens, commands. Hairline rules, 6px radius, soft offset shadows.

STORY: The visitor sees at a glance which wires are live, who is on them, and whether the line is connected; they read, post, and route with standard controls.

FIRST VIEWPORT: Left grey sidebar (wordmark, nav, live-line status at the bottom with a filled, pulsing dot when connected), content column on paper with a 56px header (page title + primary action at right). Auth pages: centred form card on paper with the wordmark and a tape strip carrying the page's one-line purpose.

FORM: Telegraph/telex wire office (tape strips, counters, form ruling), position 3 of 7 on the ordered list; seed key bd4449a0. Signature move: new live items land with a grey tape-strip highlight that settles into the paper within ~1.2 s; the connection state is the "line" indicator.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

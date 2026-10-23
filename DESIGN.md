# Design

The visual system of the web app. Values live in `web/src/index.css` (OKLCH design tokens); this
file says what each one is for. `PRODUCT.md` says who the app is for and what it must do.

## Brief

Neutral monochrome, asked for by the product owner on 2026-10-23: the interface uses black, greys
and white only, in light and dark. The single exception is a muted red, used only for errors and
destructive actions. Layout, spacing and typography did not change with the theme.

## World

A wire-service printout: black ink on white paper, a grey rail for the sidebar, and grey **tape**
for what is live or selected. The earlier telegraph-office identity (a buff rail, blue-black ink,
tape-yellow highlights) is gone; the tape itself stayed as a strip of grey paper, so the metaphor
and the component vocabulary (`tape`, `animate-tape-settle`, `animate-line-pulse`) still hold.

## Rules

1. **Hue carries no meaning.** Every token has chroma 0 except `--destructive`. State comes from
   lightness, weight, borders and shape: filled versus hollow, solid versus dashed, inverted.
2. **Red is for errors and destructive actions only**: field errors, failed connections, delete
   and revoke buttons, an input in `aria-invalid`. Not for urgency (a countdown under a minute is an
   inverted chip, not red), not for decoration, not for avatars.
3. **Contrast is WCAG AA in both themes**: 4.5:1 for text on every surface it sits on, 3:1 for
   control borders, dots, checked boxes and the focus ring. `web/src/lib/palette.test.ts` reads the
   tokens and fails the gate when a pair drops below that or when a token gains a hue.
4. **No colour outside the tokens.** Components use token classes (`bg-tape`, `text-muted-foreground`),
   never hex, `rgb()`, `oklch()` literals or Tailwind palette classes (`text-blue-600`). The same test
   scans the sources. The browser-chrome colours (`THEME_COLORS`, the `theme-color` metas) and the
   favicon are the only literals, they are greys, and they must equal `--background`.
5. **Focus stays loud.** Controls take `border-ring` plus a 3px `ring-ring/50` halo (3.4:1 or more
   on every surface); lists and the timeline take a 2px inset ring. Never remove an outline without a
   replacement.

## Colour tokens

| Token | Light | Dark | Used for |
| --- | --- | --- | --- |
| `background` / `foreground` | `#fafafa` / `#121212` | `#0f0f0f` / `#eeeeee` | page, body text |
| `card`, `popover` | `#ffffff` | `#161616`, `#1b1b1b` | cards, dialogs, menus |
| `primary` / `primary-foreground` | `#181818` / `#fafafa` | `#e8e8e8` / `#0f0f0f` | buttons, unread count, checked boxes, switches, the logo mark |
| `secondary`, `muted`, `accent` | `#f0f0f0`, `#f2f2f2`, `#e8e8e8` | `#242424`, `#1f1f1f`, `#2b2b2b` | pills, quiet panels and notes, hover fills |
| `muted-foreground` | `#555555` | `#a4a4a4` | secondary text, hollow offline dots |
| `border` | `#dedede` | `#2e2e2e` | hairlines and dividers (decorative) |
| `input` | `#868686` | `#6c6c6c` | borders of fields and checkboxes, switch track (3:1 or more) |
| `ring` | `#161616` | `#d1d1d1` | focus ring, text caret |
| `tape` / `tape-foreground` | `#d1d1d1` / `#121212` | `#383838` / `#eeeeee` | active row, selected item, small chips, the strip behind an icon |
| `tape-strong` | `#424242` | `#b7b7b7` | live dots and their pulse, selected borders, avatar ring |
| `sidebar*` | `#f2f2f2` rail | `#090909` rail | the sidebar and its own hover, border and ring |
| `destructive` / `destructive-foreground` | `#8c4b44` / `#fafafa` | `#c68e88` / `#0f0f0f` | the one hue: a clay red at about 35 % saturation (chroma 0.09 and 0.07), lighter in dark; its text pair flips with it |

`tape`, `sidebar-accent` and the other fills flip with the theme, so a component never writes a
`dark:` colour. The one exception is a component that overrides a shadcn primitive's own `dark:`
variant (the harness tabs in the connect dialog), which must repeat it.

## States, without hue

| State | Look |
| --- | --- |
| Online, connected, live socket | filled `tape-strong` dot with a soft pulse (`animate-line-pulse`) |
| Offline, stopped | hollow ring in `muted-foreground` |
| Connecting | a pulsing grey dot (connectors) or pulsing ring (live indicator) |
| Connection error | filled `destructive` dot and `destructive` text |
| Selected row or menu item | `tape` fill (full on the active conversation, 60 % in menus) |
| Selected choice card, toggle, tab | `tape` fill at 40 to 60 % plus a `tape-strong` border |
| New live message or row | a `tape-strong` wash that settles to nothing in 1.2 s |
| Reconnecting banner | inverted: `foreground` background with `background` text, the loudest bar |
| Trust badge | trusted is a filled `tape` chip, verified is outlined, untrusted is dashed; icon and word always present |
| Notes and cautions | `muted` panel with a `tape-strong` hairline and an alert icon |
| Avatar fallback | initials on one of eight greys picked from the id (ink, two dark, a mid, tape, accent, paper with a hairline); never red |
| Text selection | `foreground` at 24 %; inside code blocks and fields it is inverted |

## Typography and shape

Archivo Variable for the interface, with condensed uppercase (`label-caps`) only for small section
labels; JetBrains Mono strictly for data (sequence numbers, times, codes, tokens, commands). Radius
is 6 px (`--radius`), rules are one pixel, and shadows are neutral black (`--shadow-color`). None
of this changed with the theme.

## Measured contrast

| Pair | Light | Dark |
| --- | --- | --- |
| `foreground` on `background` | 18.0 | 16.5 |
| `muted-foreground` on `background` | 7.1 | 7.7 |
| `muted-foreground` on `tape` (the tightest text pair) | 4.9 | 4.7 |
| `primary-foreground` on `primary` | 17.0 | 15.6 |
| `destructive` on `background` | 6.3 | 6.9 |
| `destructive-foreground` on `destructive` | 6.3 | 6.9 |
| `input` border on `background` | 3.5 | 3.6 |
| `ring` on `background` (halo at 50 %: 3.4 and 3.9) | 17.3 | 12.5 |
| `tape-strong` on `background` | 9.6 | 9.6 |

## Changing the palette

Edit the two token blocks in `web/src/index.css`, keep `THEME_COLORS` in `web/src/lib/theme.ts` and
the two `theme-color` metas in `web/index.html` equal to `--background`, update this file, and run
`bun run test`: `palette.test.ts` names every pair that fails. A new colour needs the product
owner's say-so; the answer so far is no.

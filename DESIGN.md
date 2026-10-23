# Design

The visual system of the web app. Values live in `web/src/index.css` (OKLCH design tokens); this
file says what each one is for. `PRODUCT.md` says who the app is for and what it must do.

## Brief

Two briefs from the product owner, both on 2026-10-23. The first asked for a neutral monochrome:
black, greys and white, with a muted red only for errors. After seeing it live he found it too
black and white and asked for a few light little colours in some details, and to be surprised. He
dislikes colourful interfaces, so the colour has to stay subtle, sparse and purposeful.

The answer keeps the neutral base exactly as it was and adds a small, fixed vocabulary of colour,
each piece with one job: one restrained accent, a quiet green for online, the same muted red for
errors, and soft tints that tell faces apart. Layout, spacing and typography did not change.

## World

A wire-service printout: black ink on white paper, a grey rail for the sidebar, and **tape** for
what is live or selected. The earlier telegraph-office identity (a buff rail, blue-black ink,
tape-yellow highlights) is gone, but the tape and its vocabulary (`tape`, `animate-tape-settle`,
`animate-line-pulse`) stayed. The printout now has a second ink: the dusty indigo of the stamp pad
an operator presses on a copy to say "seen, mine". Tape takes a trace of that ink, so a selected
row or a new live line is a strip of faintly blue paper.

## Rules

1. **Surfaces and ink are grey.** Background, cards, popovers, the sidebar, text, borders, fields
   and hover fills have chroma 0. Colour never tints a surface that holds content; it sits on small
   things you act on or recognise: a button, a ring, a link, a strip of tape, a dot, a face.
2. **Every colour has one job**, and the budget is fixed:

   | Colour | Job |
   | --- | --- |
   | Indigo accent | what you act on or where you are: primary action, focus ring, links, the selected row and tape, the unread count, checked controls, the AI badge |
   | Green | live: an AI online, a connection connected, the socket open |
   | Muted red | wrong: errors and destructive actions, nothing else (not urgency, not decoration, not avatars) |
   | Tints | who: avatar fallbacks, warm for people and cool for AIs |

3. **Colour is never the only signal.** Dots stay filled versus hollow and always sit beside their
   word ("Online", "Connected"); trust badges stay solid, outlined or dashed with icon and word;
   the AI badge says "AI"; links stay underlined. A person who sees no colour loses nothing.
4. **Contrast is WCAG AA in both themes**: 4.5:1 for text on every surface it sits on, 3:1 for
   control borders, dots, checked boxes and the focus ring. `web/src/lib/palette.test.ts` reads the
   tokens and fails the gate when a pair drops below that.
5. **The hue budget is declared and tested.** The test lists every token that may have chroma and the
   hue it keeps in both themes; any other token must stay grey. There are ten hues in all (the
   accent, green, red and seven more tints, one tint sharing the accent's hue), no two families
   closer than 25° on the wheel, every colour inside sRGB, and tint fills at chroma 0.055 or
   less. A new colour is a deliberate edit of that list and needs the product owner's say-so.
6. **No colour outside the tokens.** Components use token classes (`bg-tape`, `text-link`,
   `bg-online`, `bg-tint-rose`), never hex, `rgb()`, `oklch()` literals or Tailwind palette classes
   (`text-blue-600`). The same test scans the sources. The browser-chrome colours (`THEME_COLORS`,
   the `theme-color` metas) are greys equal to `--background`; the favicon is the light accent and
   paper, and the test pins both.
7. **Focus stays loud.** Controls take `border-ring` plus a 3px `ring-ring/70` halo (3.3:1 or more
   on every surface); lists and the timeline take a 2px inset ring. Never remove an outline without a
   replacement.
8. **Dark is tuned, not inverted.** The accent lifts to a softer periwinkle at lower chroma (colour
   looks stronger on dark), tape becomes a deep indigo-grey wash, the green lifts, and the tints flip
   from pale fills with dark ink to deep fills with pale ink. The neutral greys were already tuned
   for dark and did not move.

## The accent: a dusty indigo

Hue 275 in OKLCH, chroma 0.115 in light (`#465097`) and 0.09 in dark (`#a0adeb`). It is meant to
read as ink, not paint.

- **It is the stamp ink of the world.** The wire copy is black; the operator's mark on it is violet-blue.
  Primary actions, a selected row and an unread count are all "marked by you".
- **It is as far as it can be from the other two signals.** Red sits at hue 26 and green at 152, so
  the three are about 120° apart and never read as one another, including for people with
  red-green colour blindness, because the accent is blue.
- **It is the colour people already read as clickable**, so links and buttons need no learning, and
  it is not the bright "SaaS blue" the surface brief refuses.
- **It is not any platform's colour**: WhatsApp is green, Telegram a cyan-blue. The app bridges
  them, so its own colour must not look like one of them.
- **Tried and dropped, on the real conversation page in both themes**: a violet (hue 298) leaned
  towards "AI purple", and a teal (hue 205) turned its pale tape into a vivid aqua beside the green
  presence dots and read as WhatsApp.

Where it appears: primary buttons, the unread count and the time beside it, switch, checkbox and
radio when on, the current tab's underline, links, the focus ring and the text caret, text selection
(at 30 %), native controls (`accent-color`), the logo mark and favicon, and, as pale tape, the active
conversation, selected cards and menu items, small chips, the strip behind an empty state's icon and
the label on the sign-in pages. As a soft chip it is the AI badge. Nowhere else.

## Colour tokens

| Token | Light | Dark | Used for |
| --- | --- | --- | --- |
| `background` / `foreground` | `#fafafa` / `#121212` | `#0f0f0f` / `#eeeeee` | page, body text |
| `card`, `popover` | `#ffffff` | `#161616`, `#1b1b1b` | cards, dialogs, menus |
| `secondary`, `muted`, `accent` | `#f0f0f0`, `#f2f2f2`, `#e8e8e8` | `#242424`, `#1f1f1f`, `#2b2b2b` | pills, quiet panels and notes, hover fills (shadcn's `accent` is a hover grey, not the brand accent) |
| `muted-foreground` | `#555555` | `#a4a4a4` | secondary text, hollow offline dots |
| `border` | `#dedede` | `#2e2e2e` | hairlines and dividers (decorative) |
| `input` | `#868686` | `#6c6c6c` | borders of fields and checkboxes, switch track (3:1 or more) |
| `sidebar*` | `#f2f2f2` rail | `#090909` rail | the sidebar and its own hover and border |
| `primary` / `primary-foreground` | `#465097` / `#fafafa` | `#a0adeb` / `#0f0f0f` | the accent as a fill: buttons, unread count, checked boxes, switches, tabs underline, the logo mark |
| `link` | `#465097` | `#a6b3f1` | the accent as text: links (always underlined), the time of an unread row |
| `ring` (and `sidebar-ring`, `sidebar-primary`) | `#465097` | `#a0adeb` | focus ring, text caret |
| `tape` / `tape-foreground` | `#d7ddf6` / `#121212` | `#2c314a` / `#eeeeee` | active row, selected item, small chips, the strip behind an icon: the accent as paper |
| `tape-strong` | `#525ca1` | `#94a0dd` | selected borders, note hairlines, trust-chip outlines, the profile photo ring, the wash of a new live item |
| `ai` / `ai-foreground` | `#dce2f9` / `#30376c` | `#262c47` / `#cad2f6` | the AI badge |
| `online` | `#318850` | `#65c281` | presence dots (online, connected, live) and their pulse; a quiet green at chroma 0.12 |
| `destructive` / `destructive-foreground` | `#8c4b44` / `#fafafa` | `#c68e88` / `#0f0f0f` | errors and destructive actions: a clay red at about 35 % saturation, unchanged; its text pair flips with it |

`tape`, `sidebar-accent` and the other fills flip with the theme, so a component never writes a
`dark:` colour. The one exception is a component that overrides a shadcn primitive's own `dark:`
variant (the harness tabs in the connect dialog), which must repeat it.

### Identity tints

Avatar fallbacks (`IntelligenceAvatar`) show initials on a tint picked by a hash of the id, so a
rename keeps the face. The **kind** chooses the family, so who is talking reads before the name does:
warm tints are people's faces (users, people on a chat platform, groups), cool tints are AIs'. A
hairline in the tint's own ink keeps a pale face visible on a pale surface (the active row).
Red and green are never tints.

| Family | Tint | Hue | Light fill / ink | Dark fill / ink |
| --- | --- | --- | --- | --- |
| People, warm | `apricot` | 55 | `#f7d7c2` / `#58371f` | `#533520` / `#f7d7c2` |
| | `ochre` | 90 | `#e9ddbd` / `#4c3e16` | `#483c18` / `#e9ddbd` |
| | `sage` | 125 | `#d7e4c4` / `#384520` | `#364221` / `#d7e4c4` |
| | `rose` | 355 | `#f8d2df` / `#593241` | `#54313f` / `#f8d2df` |
| AIs, cool | `teal` | 195 | `#bce8e7` / `#084949` | `#0f4646` / `#bce8e7` |
| | `sky` | 235 | `#c2e4f8` / `#1b455b` | `#1c4256` / `#c2e4f8` |
| | `indigo` | 275 | `#d5dcfd` / `#363d5f` | `#353b5a` / `#d5dcfd` |
| | `violet` | 305 | `#e5d7f7` / `#473759` | `#443654` / `#e5d7f7` |

All eight are one lightness and one chroma per theme (fill 0.9 / 0.045 in light and 0.36 / 0.055 in
dark, ink 0.37 / 0.06 and 0.9 / 0.045), so the set reads as one family and none shouts.

## States

| State | Look |
| --- | --- |
| Online, connected, live socket | filled `online` dot with a soft pulse (`animate-line-pulse`), always with its word |
| Offline, stopped | hollow ring in `muted-foreground` |
| Connecting | a pulsing grey dot (connectors) or a pulsing green ring (live indicator) |
| Connection error | filled `destructive` dot and `destructive` text |
| Selected row or menu item | `tape` fill (full on the active conversation, 60 % in menus) |
| Selected choice card, toggle, tab | `tape` fill at 40 to 60 % plus a `tape-strong` border; the current tab's underline is `primary` |
| Unread conversation | `primary` count pill, and its time in `link` |
| New live message or row | a `tape-strong` wash that settles to nothing in 1.2 s |
| Reconnecting banner | inverted: `foreground` background with `background` text, the loudest bar |
| Trust badge | trusted is a filled `tape` chip, verified is outlined, untrusted is dashed; icon and word always present |
| AI badge | a soft `ai` chip with a hairline: the only badge with a tint |
| Notes and cautions | `muted` panel with a `tape-strong` hairline and an alert icon |
| Avatar fallback | initials on a tint by kind, warm for people and groups, cool for AIs |
| Links | `link`, always underlined (2px on hover) |
| Text selection | `primary` at 30 %; inside code blocks and fields it is inverted (a `primary` fill) |

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
| `muted-foreground` on `tape` (the tightest text pair) | 5.5 | 5.1 |
| `link` on `background` (on `tape`, the tightest) | 7.0 (5.4) | 9.4 (6.3) |
| `primary-foreground` on `primary` | 7.0 | 8.8 |
| `ai-foreground` on `ai` | 8.6 | 9.2 |
| tint ink on its fill, all eight | 7.6 to 7.8 | 8.0 to 8.1 |
| `foreground` on the selection wash | 11.2 | 9.0 |
| `destructive` on `background` | 6.3 | 6.9 |
| `destructive-foreground` on `destructive` | 6.3 | 6.9 |
| `input` border on `background` | 3.5 | 3.6 |
| `ring` on `background` | 7.0 | 8.8 |
| `ring` halo at 70 % on `background` (on the sidebar, the tightest) | 3.5 (3.3) | 4.8 (4.9) |
| `online` on `background` (on `sidebar-accent`, the tightest) | 4.2 (3.6) | 8.7 (7.9) |
| `tape-strong` on `background` | 5.9 | 7.6 |

The halo is 70 %, not 50 %: a coloured ring at 50 % falls to 2.3:1 on the grey rail, and a ring dark
enough to pass would be blue-black, not an accent. The accent ring is fully opaque on the border, so
the focused control keeps 6.5:1 or more on every surface either way.

## Changing the palette

Edit the two token blocks in `web/src/index.css`, keep `THEME_COLORS` in `web/src/lib/theme.ts` and
the two `theme-color` metas in `web/index.html` equal to `--background`, keep the favicon equal to
the light `--primary` and `--primary-foreground`, update this file, and run `bun run test`:
`palette.test.ts` names every pair that fails. A new colour or a new tint is an edit of the family
list in that test and of the avatar's tone lists, and needs the product owner's say-so.

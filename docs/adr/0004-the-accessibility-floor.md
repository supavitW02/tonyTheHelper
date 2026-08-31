# The accessibility floor

The app's primary user is elderly, so readability is a functional requirement rather than
a nicety. These are the floors, and they are enforced by measurement rather than by
intention:

| | Floor | Measured |
| --- | --- | --- |
| Body text size | 20px | 20px is the smallest text anywhere on screen |
| Contrast, live text | 4.5:1 (WCAG AA) | lowest is 6.01:1 |
| Interactive control size | 48px on both axes | smallest is 97×60px |
| Step (−/+) buttons | 48px | 70×70px |
| UI boundaries (input borders) | 3:1 (WCAG 1.4.11) | 4.20:1 |
| Horizontal overflow at 375px | none | none |

Issue #1 set 20px as a guess. Issue #12 validated it on the intended user's own phone;
he read it comfortably, so the guess stands rather than being revised upward. Issue #26
re-measured everything after the palette changed from neutral slate to the brand greens.

## How the numbers are produced

The root font size is `125%`, not `20px`. A percentage inherits the browser's own setting,
so a reader who has already enlarged their system font keeps that enlargement — a fixed
pixel value would silently override it. 16px × 125% = 20px, and every `rem`-based size in
the app scales from there. This is also why the step buttons are 70px rather than the 56px
`size-14` suggests: 3.5rem at a 20px root.

Contrast is measured in Chrome across every text-bearing element, in every state the
screen has — both tabs, each item panel, and each dialog open — normalising colours
through a canvas, because Tailwind v4 emits `oklch()` values that a naive `rgb()` parser
silently misreads as white and then reports a clean pass.

Two measurement traps, both hit for real:

- **The viewport must be emulated, not resized.** Asking Chrome for a 375px-wide window
  gave a 500px CSS viewport, because the window cannot get that narrow on a desktop. A
  `92vw` dialog then measured 460px and every "fits on a phone" claim was worthless. Use
  device emulation so `window.innerWidth` actually reports 375.
- **The frontend is compiled into the Go binary** (ADR-0003), so rebuilding `web/` changes
  nothing that a browser can see. The binary has to be rebuilt before re-measuring, or the
  measurement describes the previous palette.

## Consequences

- **Density is not available as a design move.** Anything that shrinks type or tightens
  tap targets to fit more on screen breaks the reason this app exists.
- **The logo's own greens cannot carry white text.** Sampled from the artwork, the lime
  `#b8d858` gives 1.62:1 behind white and the mid green `#488828` gives 4.35:1 — under the
  floor. So the bright end of the brand scale is decorative only, and every filled control
  uses `brand-700` (6.01:1) or darker. A brand palette does not get to override the floor.
- **Input borders are `brand-600`, the lightest green that clears 3:1.** `brand-500`
  measured 2.97:1, missing it by 0.03. The previous `slate-300` borders were 1.48:1 — the
  edge of an input box was, strictly, invisible.
- **Short labels need padding for the finger.** "แก้ไข" is only 43px wide as bare
  underlined text, under the 48px floor on one axis. The link actions carry `px-2 -mx-2`:
  the padding grows the tap target, the negative margin keeps the text aligned. Measure
  both axes — an earlier pass only recorded height and read as passing.
- **Disabled controls are exempt and deliberately below the floor.** The `−` button at
  quantity zero measures 2.13:1, and a disabled submit measures 3.86:1. WCAG excludes
  disabled controls, and looking unavailable is the point.
- **The floor is checkable.** Re-running the measurement is how a change is judged, not an
  opinion about whether it "looks fine".

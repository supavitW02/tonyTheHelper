# The accessibility floor

The app's primary user is elderly, so readability is a functional requirement rather than
a nicety. These are the floors, and they are enforced by measurement rather than by
intention:

| | Floor | Measured |
| --- | --- | --- |
| Body text size | 20px | 20px is the smallest text anywhere on screen |
| Contrast, live text | 4.5:1 (WCAG AA) | lowest is 6.14:1 |
| Interactive control height | 48px | smallest is 60px |
| Step (−/+) buttons | 48px | 70×70px |
| Horizontal overflow at 375px | none | none |

Issue #1 set 20px as a guess. Issue #12 validated it on the intended user's own phone;
he read it comfortably, so the guess stands rather than being revised upward.

## How the numbers are produced

The root font size is `125%`, not `20px`. A percentage inherits the browser's own setting,
so a reader who has already enlarged their system font keeps that enlargement — a fixed
pixel value would silently override it. 16px × 125% = 20px, and every `rem`-based size in
the app scales from there. This is also why the step buttons are 70px rather than the 56px
`size-14` suggests: 3.5rem at a 20px root.

Contrast was measured in Chrome at 375×812 across every text-bearing element with all
panels open, normalising colours through a canvas because Tailwind v4 emits `oklch()`
values that a naive `rgb()` parser silently misreads as white.

## Consequences

- **Density is not available as a design move.** Anything that shrinks type or tightens
  tap targets to fit more on screen breaks the reason this app exists.
- **Informational text is `slate-600`, not `slate-500`.** On a `slate-50` panel, slate-500
  measured 4.55:1 — passing AA by 0.05, so any future lightening would fail it. slate-600
  gives 7.25:1 there and moves the app's floor to 6.14:1.
- **Disabled controls are exempt and deliberately below the floor.** The `−` button at
  quantity zero measures 2.13:1. WCAG excludes disabled controls, and looking unavailable
  is the point.
- **The floor is checkable.** Re-running the measurement is how a change is judged, not an
  opinion about whether it "looks fine".

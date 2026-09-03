import { useEffect, useState } from 'react'

import { ArrowUpIcon } from './icons'

/**
 * How far down the page has to be before the button earns the space it covers.
 *
 * Roughly one screen. Below that the top of the page is still in view and a button
 * offering to take you there would be answering a question nobody asked.
 */
const SHOW_AFTER = 320

/**
 * Back to the top of the stock list, without twenty flicks of the thumb.
 *
 * The list this floats over is the one screen in the app that gets genuinely long: a
 * household with thirty items, each a card with a photo and two 70px buttons, is several
 * screens of scrolling, and the things at the top of it — the add button and the search
 * box — are the ones you reach for next.
 *
 * It sits above the bottom bar rather than beside it, and is pinned to the same 560px
 * column as the content so it stays next to the list on a wide screen instead of drifting
 * off to the edge of the window.
 *
 * 60px across rather than the 70px of the app's other round buttons. It is the one control
 * that covers something else while it is on screen, so it is sized to the 48px floor in
 * ADR-0004 with room to spare rather than to the step buttons it floats over.
 */
export default function ScrollToTop() {
  const [shown, setShown] = useState(false)

  useEffect(() => {
    const onScroll = () => setShown(window.scrollY > SHOW_AFTER)
    // Read once on mount: switching back to this tab part-way down the page has to find
    // the button already there, and no scroll event is coming to tell it.
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  if (!shown) return null

  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-[calc(var(--nav-height)_+_1rem)] z-20 mx-auto flex max-w-md justify-end px-6">
      <button
        type="button"
        // The arrow is decoration; this is the only name a screen reader gets.
        aria-label="เลื่อนขึ้นบนสุด"
        onClick={() => {
          // Smooth is a nicety, not a requirement — someone who has asked their phone to
          // stop animating things gets the jump instead.
          const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
          window.scrollTo({ top: 0, behavior: reduced ? 'auto' : 'smooth' })
        }}
        // The white ring is not trim. This floats over the item cards, and the thing it
        // most often lands on is a card's own green + button — without a hard edge of
        // background colour between them it reads as part of the card, which is exactly
        // the button someone must not tap by mistake.
        className="pointer-events-auto flex size-12 items-center justify-center rounded-full bg-brand-700 text-white shadow-lg ring-4 ring-white"
      >
        <ArrowUpIcon className="size-6" />
      </button>
    </div>
  )
}

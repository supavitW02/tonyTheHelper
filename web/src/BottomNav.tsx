import { useRef } from 'react'

import { HistoryIcon, HomeIcon, SettingsIcon } from './icons'

/**
 * The three places the app goes, and the only navigation it has.
 *
 * Labels are one word each on purpose. The strip this replaced could only hold two,
 * because "ประวัติของใช้" is 11 characters and three of those do not fit across 375px.
 * With an icon carrying the meaning the label can shrink to "ประวัติ" and a third
 * destination fits — which is how the two errands that used to float above the screen,
 * managing the household and switching between households, got somewhere to live.
 */
const TABS = [
  { id: 'items', label: 'ของใช้', Icon: HomeIcon },
  { id: 'history', label: 'ประวัติ', Icon: HistoryIcon },
  { id: 'settings', label: 'ตั้งค่า', Icon: SettingsIcon },
] as const

export type Tab = (typeof TABS)[number]['id']

/**
 * The bar pinned to the bottom of the screen.
 *
 * Bottom rather than top because this is a phone held in one hand: the foot of the screen
 * is where the thumb already is, and the top of a 6-inch screen is where it is not.
 *
 * It is a tablist rather than a nav, because that is what it is — the three panels are all
 * in this one document and the URL never changes, so calling them links would promise a
 * page load that never happens. The role also buys the left/right arrow keys for free.
 *
 * Which one is selected is said three ways — a tinted cell, a heavier weight, and a darker
 * green — because colour alone is not a state anyone with low vision can read (WCAG 1.4.1),
 * and this app's reader is elderly by design.
 */
export default function BottomNav({ tab, onChange }: { tab: Tab; onChange: (tab: Tab) => void }) {
  const refs = useRef<Partial<Record<Tab, HTMLButtonElement | null>>>({})

  /** Left/right arrows move between tabs, which is what the tab role promises. */
  function onKeyDown(event: React.KeyboardEvent) {
    const step = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    if (step === 0) return

    event.preventDefault()
    const at = TABS.findIndex((t) => t.id === tab)
    const next = TABS[(at + step + TABS.length) % TABS.length].id
    onChange(next)
    refs.current[next]?.focus()
  }

  return (
    // The padding is the phone's own home indicator, so the bottom row of labels is not
    // sitting underneath it. It resolves to zero on anything without one.
    <div className="fixed inset-x-0 bottom-0 z-30 border-t-2 border-brand-200 bg-white pb-[env(safe-area-inset-bottom)]">
      <div
        role="tablist"
        aria-label="มุมมองของบ้าน"
        onKeyDown={onKeyDown}
        className="mx-auto flex max-w-md"
      >
        {TABS.map(({ id, label, Icon }) => {
          const selected = id === tab
          return (
            <button
              key={id}
              id={`tab-${id}`}
              ref={(el) => {
                refs.current[id] = el
              }}
              type="button"
              role="tab"
              aria-selected={selected}
              aria-controls={`panel-${id}`}
              // Roving tabindex: the whole bar is one tab stop, arrows move within it.
              tabIndex={selected ? 0 : -1}
              onClick={() => onChange(id)}
              className={`flex min-h-14 flex-1 flex-col items-center justify-center gap-1 px-2 py-1 ${
                selected ? 'bg-brand-100 font-semibold text-brand-800' : 'font-medium text-slate-600'
              }`}
            >
              <Icon className="size-5" strokeWidth={selected ? 2.5 : 2} />
              {/* leading-none: the label's own line box is the only slack left in a bar
                  that is already down to the 20px type floor plus a 25px icon. */}
              <span className="leading-none">{label}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}

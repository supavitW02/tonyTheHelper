import { useEffect, useId, useRef, useState } from 'react'

/*
  The shared look of every control, kept here rather than repeated per component.

  Two rules hold across all of it, both from ADR-0004: nothing interactive is shorter
  than min-h-12 (48px at this root font size), and no colour pairing drops below 4.5:1.
  That is why filled surfaces use brand-700 or darker even though the logo's own greens
  are brighter — the bright end of the scale cannot carry white text.
*/

/** Text inputs. The border is brand-600, the lightest green that clears 3:1 against
 *  white, so the edge of the box is actually findable. */
export const inputClass =
  'min-h-14 w-full rounded-xl border-2 border-brand-600 bg-white px-4 py-3 focus:border-brand-800 focus:ring-4 focus:ring-brand-200 focus:outline-none'

/** The one obvious action on a screen. */
export const primaryButtonClass =
  'min-h-14 rounded-xl bg-brand-700 px-4 py-3 font-medium text-white disabled:bg-slate-200 disabled:text-slate-500'

/** The way out: cancel, dismiss, go back. */
export const secondaryButtonClass =
  'min-h-14 rounded-xl border-2 border-brand-600 px-4 py-3 font-medium text-brand-800 disabled:border-slate-300 disabled:text-slate-500'

/** Removing or deleting. Red, and never the primary action on a screen. */
export const dangerButtonClass =
  'min-h-14 rounded-xl bg-red-700 px-4 py-3 font-medium text-white disabled:bg-slate-200 disabled:text-slate-500'

/**
 * A secondary action that reads as a link. Blue, from the logo's apron.
 *
 * The horizontal padding is there for the finger, not the eye: a short Thai label like
 * "แก้ไข" is only 43px wide, under the 48px floor. The matching negative margin cancels
 * the padding out of the layout, so the tap area grows without the text moving.
 */
export const linkActionClass = '-mx-2 min-h-12 px-2 text-accent-700 underline underline-offset-4'

/** A labelled form row. */
export function Field({
  label,
  htmlFor,
  children,
}: {
  label: string
  htmlFor: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={htmlFor} className="font-medium">
        {label}
      </label>
      {children}
    </div>
  )
}

/**
 * A LINE profile picture, with a stand-in for when there isn't one.
 *
 * The fallback is not decoration. These URLs point at LINE's CDN, which the app does not
 * control: a picture that loaded last week can start failing, and a member may have no
 * picture at all. Without this, either case leaves a broken-image icon beside someone's
 * name. Rendering the stand-in at the same size also means the row does not jump when a
 * picture finishes loading.
 */
export function Avatar({ src, className }: { src?: string; className: string }) {
  const [failed, setFailed] = useState(false)

  if (!src || failed) {
    return (
      <span
        aria-hidden="true"
        className={`flex shrink-0 items-center justify-center rounded-full bg-brand-100 ${className}`}
      >
        👤
      </span>
    )
  }
  return (
    <img
      src={src}
      alt=""
      onError={() => setFailed(true)}
      // LINE's CDN rejects requests that carry a referrer from another origin.
      referrerPolicy="no-referrer"
      className={`shrink-0 rounded-full bg-brand-100 object-cover ${className}`}
    />
  )
}

/**
 * Something the user needs to be told.
 *
 * `error` interrupts a screen reader mid-sentence because it means an action did not
 * happen; `info` waits its turn because it is only progress.
 */
export function Notice({ tone = 'error', children }: { tone?: 'error' | 'info'; children: React.ReactNode }) {
  if (tone === 'info') {
    return (
      <p aria-live="polite" className="flex min-h-12 items-center rounded-xl bg-brand-50 px-4 py-3 text-brand-900">
        {children}
      </p>
    )
  }
  return (
    <p
      aria-live="assertive"
      className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900"
    >
      <span aria-hidden="true" className="text-2xl leading-none">
        ✕
      </span>
      <span>{children}</span>
    </p>
  )
}

/**
 * A modal overlay, built on the native `<dialog>` element.
 *
 * Native rather than a hand-rolled div because `showModal()` already does the parts that
 * are easy to get wrong: it moves focus into the dialog, traps it there, makes everything
 * behind it inert, and puts the dialog in the browser's top layer so no z-index can lose
 * to it.
 *
 * Note what is deliberately missing: tapping the backdrop does not close it. A native
 * dialog only closes on Esc, and that is the behaviour we want — someone half-way through
 * typing an item must not lose it to a stray tap beside the box. Leaving is done with the
 * cancel button.
 */
export function Dialog({
  title,
  onClose,
  initialFocus,
  children,
}: {
  title: string
  onClose: () => void
  /**
   * What should hold focus when the dialog opens, instead of the first focusable element.
   *
   * Needed for a destructive dialog, where the first thing in the DOM is the button you
   * do not want a stray Enter to press. Note that React's `autoFocus` cannot do this
   * job: React implements it by calling focus() during commit, and `showModal()` runs
   * after that and moves focus to the first focusable element regardless.
   */
  initialFocus?: React.RefObject<HTMLElement | null>
  children: React.ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()

  useEffect(() => {
    const el = ref.current
    if (!el) return
    el.showModal()
    initialFocus?.current?.focus()
    return () => {
      el.close()
    }
  }, [initialFocus])

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      // Esc fires `cancel`. Preventing the default close and routing through onClose
      // keeps React the one deciding whether the dialog is mounted, so the DOM and the
      // component state cannot disagree about whether it is open.
      onCancel={(event) => {
        event.preventDefault()
        onClose()
      }}
      className="m-auto max-h-[88dvh] w-[92vw] max-w-md overflow-y-auto rounded-2xl bg-white p-5 text-brand-950 backdrop:bg-brand-950/50"
    >
      <h2 id={titleId} className="mb-5 text-2xl font-semibold text-brand-800">
        {title}
      </h2>
      {children}
    </dialog>
  )
}

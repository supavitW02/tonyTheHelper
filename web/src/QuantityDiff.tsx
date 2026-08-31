/**
 * The size and direction of one quantity change: `+1`, `−2`.
 *
 * A history row already says "3 → 4", but reading a direction out of two numbers is work,
 * and a list of thirty of them is a lot of work. This says which way it went at a glance.
 *
 * Colour is deliberately not the message. A green tint and a red tint of this lightness
 * are within 1.05:1 of each other, so anyone who cannot separate the two hues gets nothing
 * at all from them. The + or − sign is what carries the meaning; the colour is a second,
 * faster channel for everyone else.
 */
export default function QuantityDiff({ before, after }: { before: number; after: number }) {
  const change = after - before
  // A movement is only ever recorded when something actually moved (ADR-0001), so a zero
  // change should not reach here. Rendering nothing is the honest response if it does.
  if (change === 0) return null

  const up = change > 0
  return (
    <span
      // The sign is spelled out for a screen reader rather than left as a glyph: "−" is
      // U+2212, which is read inconsistently, and a bare number out of context says
      // nothing about direction.
      aria-label={up ? `เพิ่มขึ้น ${change}` : `ลดลง ${Math.abs(change)}`}
      className={`inline-flex items-center rounded-full px-3 font-medium whitespace-nowrap ${
        up ? 'bg-brand-100 text-brand-800' : 'bg-red-100 text-red-800'
      }`}
    >
      {up ? '+' : '−'}
      {Math.abs(change)}
    </span>
  )
}

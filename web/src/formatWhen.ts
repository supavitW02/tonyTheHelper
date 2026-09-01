/**
 * When something happened, in Thai, as short as it can be without losing meaning.
 *
 * The year is dropped for anything from the current year — which is nearly everything a
 * history list shows — and kept otherwise. That matters for layout rather than taste: the
 * name and the timestamp now share a line, and "30 ส.ค. 2569 08:56" alongside a Thai name
 * does not fit across a phone at this type size. th-TH uses the Buddhist calendar, so the
 * year still reads as 2569 when it does appear.
 */
export function formatWhen(iso: string): string {
  const at = new Date(iso)
  const sameYear = at.getFullYear() === new Date().getFullYear()

  return at.toLocaleString('th-TH', {
    day: 'numeric',
    month: 'short',
    ...(sameYear ? {} : { year: 'numeric' }),
    hour: '2-digit',
    minute: '2-digit',
  })
}

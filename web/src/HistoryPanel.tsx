import { useCallback, useEffect, useState } from 'react'

import { getHouseholdHistory, type Household, type HouseholdMovement, type ItemRef } from './api'
import QuantityDiff from './QuantityDiff'
import { inputClass, linkActionClass, Notice, primaryButtonClass } from './ui'

/** What the list currently reflects, as opposed to what is typed into the controls. */
type Applied = { itemId: string; q: string }

const NOTHING_APPLIED: Applied = { itemId: '', q: '' }

const isFiltered = (a: Applied) => a.itemId !== '' || a.q !== ''

/**
 * The household's recent history: every item's changes in one list, newest first.
 *
 * A single item's panel answers "what happened to this jar". This answers the question
 * you cannot ask there — "what happened in this house today" — which is the one someone
 * actually has when they get home and the shelf looks different.
 *
 * No pictures, on purpose. This is read down rather than looked at, and a column of
 * thumbnails would make thirty rows into a very long scroll.
 */
export default function HistoryPanel({ household, active }: { household: Household; active: boolean }) {
  const [movements, setMovements] = useState<HouseholdMovement[] | null>(null)
  const [items, setItems] = useState<ItemRef[]>([])
  const [error, setError] = useState<string | null>(null)
  const [searching, setSearching] = useState(false)

  // The controls hold a draft until the user asks for it. Nothing moves under their
  // finger while they are still choosing — the list only changes when they say so.
  const [draftItemId, setDraftItemId] = useState('')
  const [draftQuery, setDraftQuery] = useState('')
  const [applied, setApplied] = useState<Applied>(NOTHING_APPLIED)

  const load = useCallback(
    async (filter: Applied) => {
      setSearching(true)
      setError(null)
      try {
        const { movements, items } = await getHouseholdHistory(household.id, {
          itemId: filter.itemId || undefined,
          q: filter.q || undefined,
        })
        setMovements(movements)
        // The choices are not narrowed by the search: the filter is how someone changes
        // their mind, so it has to keep offering everything.
        setItems(items)
        setApplied(filter)
      } catch (err) {
        console.error('load history failed', err)
        setError('โหลดประวัติไม่สำเร็จ')
      } finally {
        setSearching(false)
      }
    },
    [household.id],
  )

  // Mounted with the other tabs so the tab strip's aria-controls always resolves, but the
  // request waits until someone actually opens it.
  useEffect(() => {
    if (!active || movements !== null) return
    load(NOTHING_APPLIED).catch(() => {})
  }, [active, movements, load])

  function search(event: React.FormEvent) {
    event.preventDefault()
    if (searching) return
    load({ itemId: draftItemId, q: draftQuery.trim() }).catch(() => {})
  }

  function clear() {
    setDraftItemId('')
    setDraftQuery('')
    load(NOTHING_APPLIED).catch(() => {})
  }

  const filteredName = items.find((i) => i.id === applied.itemId)?.name

  return (
    <section className="flex flex-col gap-5">
      <form onSubmit={search} className="flex flex-col gap-3">
        <label htmlFor="history-search" className="font-medium">
          ค้นหาชื่อของหรือหมายเหตุ
        </label>
        <input
          id="history-search"
          type="search"
          value={draftQuery}
          onChange={(e) => setDraftQuery(e.target.value)}
          maxLength={100}
          autoComplete="off"
          placeholder="เช่น น้ำปลา"
          className={inputClass}
        />

        <label htmlFor="history-item" className="font-medium">
          เลือกของ
        </label>
        <select
          id="history-item"
          value={draftItemId}
          onChange={(e) => setDraftItemId(e.target.value)}
          className={inputClass}
        >
          <option value="">ทุกรายการ</option>
          {items.map((i) => (
            <option key={i.id} value={i.id}>
              {i.name}
            </option>
          ))}
        </select>

        <button type="submit" disabled={searching} className={primaryButtonClass}>
          {searching ? 'กำลังค้นหา…' : 'ค้นหา'}
        </button>
      </form>

      {error && <Notice>{error}</Notice>}

      {/* Being in a filtered view has to be obvious, and getting out of it has to be one
          tap: a list that silently hides most of the history is worse than no filter. */}
      {isFiltered(applied) && (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
          <span className="text-slate-600">
            {filteredName ? `เฉพาะ “${filteredName}”` : 'ผลการค้นหา'}
            {applied.q && ` · คำค้น “${applied.q}”`}
          </span>
          <button type="button" onClick={clear} className={linkActionClass}>
            ดูทั้งหมด
          </button>
        </div>
      )}

      {movements === null && !error && <Notice tone="info">กำลังโหลดประวัติ…</Notice>}

      {movements?.length === 0 && (
        <p className="rounded-xl bg-brand-50 px-4 py-8 text-center text-slate-600">
          {isFiltered(applied) ? 'ไม่พบประวัติที่ค้นหา' : 'ยังไม่มีการเปลี่ยนแปลงในบ้านนี้'}
        </p>
      )}

      {movements && movements.length > 0 && (
        <>
          <ul aria-label="ประวัติของใช้" className="flex flex-col gap-3">
            {movements.map((m) => (
              <li key={m.id} className="flex flex-col gap-1 rounded-xl border-2 border-brand-200 p-4">
                <span className="break-words">
                  <strong className="font-medium">{m.itemName}</strong>{' '}
                  <span className="whitespace-nowrap">
                    {m.quantityBefore} → {m.quantityAfter} {m.unit}
                  </span>{' '}
                  <QuantityDiff before={m.quantityBefore} after={m.quantityAfter} />
                </span>
                <span className="text-slate-600">
                  {m.actorName} ·{' '}
                  {new Date(m.createdAt).toLocaleString('th-TH', { dateStyle: 'medium', timeStyle: 'short' })}
                </span>
                {m.note && <span className="break-words text-slate-600">“{m.note}”</span>}
              </li>
            ))}
          </ul>

          {/* Say so when the list is capped, rather than letting it look complete. */}
          {movements.length >= HOUSEHOLD_HISTORY_LIMIT && (
            <p className="text-slate-600">แสดง {HOUSEHOLD_HISTORY_LIMIT} รายการล่าสุด ค้นหาเพื่อดูรายการอื่น</p>
          )}
        </>
      )}
    </section>
  )
}

/** Mirrors HouseholdHistoryLimit in the API, which is what actually caps the list. */
const HOUSEHOLD_HISTORY_LIMIT = 30

import { useCallback, useEffect, useState } from 'react'

import { adjustQuantity, getItems, type Household, type Item, type User } from './api'
import AddItemForm from './AddItemForm'
import ItemRow from './ItemRow'

/** The household someone lands in: its stock, and the controls to change it. */
export default function HouseholdScreen({ household, user }: { household: Household; user: User }) {
  const [items, setItems] = useState<Item[] | null>(null)
  const [units, setUnits] = useState<string[]>([])
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Requests in flight per item. While any are outstanding the displayed quantity is
  // optimistic, so a rapid tapper is not left waiting on the network between taps.
  const [, setPending] = useState<Record<string, number>>({})

  const reload = useCallback(async () => {
    const { items, units } = await getItems(household.id)
    setItems(items)
    setUnits(units)
  }, [household.id])

  useEffect(() => {
    reload().catch((err: unknown) => {
      console.error('load items failed', err)
      setError('โหลดรายการไม่สำเร็จ')
    })
  }, [reload])

  /**
   * Applies a quantity change optimistically, then reconciles with the server.
   *
   * The server value is only adopted once nothing else is in flight for that item:
   * responses to rapid taps can arrive out of order, and adopting a stale one would make
   * the number jump backwards under the user's finger.
   */
  async function change(item: Item, optimistic: number, request: Promise<Item>) {
    setError(null)
    setItems((current) => current?.map((i) => (i.id === item.id ? { ...i, quantity: optimistic } : i)) ?? null)
    setPending((p) => ({ ...p, [item.id]: (p[item.id] ?? 0) + 1 }))

    try {
      const saved = await request
      setPending((p) => {
        const left = (p[item.id] ?? 1) - 1
        if (left === 0) {
          setItems((current) => current?.map((i) => (i.id === item.id ? saved : i)) ?? null)
        }
        return { ...p, [item.id]: left }
      })
    } catch (err) {
      console.error('adjust quantity failed', err)
      setPending((p) => ({ ...p, [item.id]: Math.max(0, (p[item.id] ?? 1) - 1) }))
      setError('บันทึกจำนวนไม่สำเร็จ')
      // The optimistic number is now a lie; the server is the truth.
      reload().catch(() => {})
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <header className="flex items-center gap-3">
        {user.pictureUrl && (
          <img src={user.pictureUrl} alt="" className="size-10 shrink-0 rounded-full object-cover" referrerPolicy="no-referrer" />
        )}
        <div className="flex min-w-0 flex-col">
          <h2 className="truncate text-2xl font-semibold">{household.name}</h2>
          {household.isOwner && <span className="text-slate-500">เจ้าของบ้าน</span>}
        </div>
      </header>

      {error && (
        <p aria-live="assertive" className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900">
          <span aria-hidden="true" className="text-2xl leading-none">
            ✕
          </span>
          <span>{error}</span>
        </p>
      )}

      {items === null && (
        <p aria-live="polite" className="flex min-h-12 items-center rounded-xl bg-slate-100 px-4 py-3 text-slate-700">
          กำลังโหลดรายการ…
        </p>
      )}

      {items?.length === 0 && !adding && (
        <section className="flex flex-col items-center gap-3 rounded-xl bg-slate-50 px-6 py-10 text-center">
          <span aria-hidden="true" className="text-4xl">
            🧺
          </span>
          <p className="text-slate-600">ยังไม่มีของในบ้านนี้</p>
        </section>
      )}

      {items && items.length > 0 && (
        <ul aria-label="รายการของใช้" className="flex flex-col gap-3">
          {items.map((item) => (
            <ItemRow
              key={item.id}
              item={item}
              onStep={(delta) =>
                change(item, Math.max(0, item.quantity + delta), adjustQuantity(item.id, { delta }))
              }
              onSetTotal={(quantity, note) => change(item, quantity, adjustQuantity(item.id, { quantity, note }))}
            />
          ))}
        </ul>
      )}

      {adding ? (
        <AddItemForm
          householdId={household.id}
          units={units}
          onCancel={() => setAdding(false)}
          onAdded={(item) => {
            setAdding(false)
            setItems((current) => [...(current ?? []), item].sort((a, b) => a.name.localeCompare(b.name, 'th')))
            if (!units.includes(item.unit)) setUnits((u) => [...u, item.unit].sort())
          }}
        />
      ) : (
        items !== null && (
          <button
            type="button"
            onClick={() => setAdding(true)}
            className="min-h-14 rounded-xl bg-slate-900 px-4 py-3 font-medium text-white"
          >
            {items.length === 0 ? 'เพิ่มของชิ้นแรก' : 'เพิ่มของ'}
          </button>
        )
      )}
    </div>
  )
}

import { useCallback, useEffect, useRef, useState } from 'react'

import {
  adjustQuantity,
  deleteItem,
  getItems,
  updateItem,
  uploadItemPhoto,
  type Household,
  type Item,
  type User,
} from './api'
import AddItemForm from './AddItemForm'
import ItemRow from './ItemRow'
import MembersPanel from './MembersPanel'
import RenameHousehold from './RenameHousehold'
import { Avatar, linkActionClass, Notice, primaryButtonClass } from './ui'

/**
 * The two things a household screen is about. They used to share one scroll, with the
 * member list expanding in the middle of it and shoving the stock list down the page.
 * Tabs give each one the whole screen and stop either from moving the other.
 */
const TABS = [
  { id: 'items', label: 'ของใช้' },
  { id: 'members', label: 'สมาชิก' },
] as const

type Tab = (typeof TABS)[number]['id']

/**
 * Orders items the way the server does: low stock first so it is noticed before running
 * out, then alphabetically. Kept in step with the SQL ordering in the store.
 */
function sortItems(items: Item[]): Item[] {
  return [...items].sort((a, b) => {
    if (a.isLow !== b.isLow) return a.isLow ? -1 : 1
    return a.name.localeCompare(b.name, 'th')
  })
}

/** The household someone lands in: its stock, and the controls to change it. */
export default function HouseholdScreen({
  household,
  user,
  onRenamed,
}: {
  household: Household
  user: User
  onRenamed: (household: Household) => void
}) {
  const [tab, setTab] = useState<Tab>('items')
  const [items, setItems] = useState<Item[] | null>(null)
  const [units, setUnits] = useState<string[]>([])
  const [adding, setAdding] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Requests in flight per item. While any are outstanding the displayed quantity is
  // optimistic, so a rapid tapper is not left waiting on the network between taps.
  const [, setPending] = useState<Record<string, number>>({})
  const tabRefs = useRef<Partial<Record<Tab, HTMLButtonElement | null>>>({})

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

  /** Left/right arrows move between tabs, which is what the tab role promises. */
  function onTabKeyDown(event: React.KeyboardEvent) {
    const step = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    if (step === 0) return

    event.preventDefault()
    const at = TABS.findIndex((t) => t.id === tab)
    const next = TABS[(at + step + TABS.length) % TABS.length].id
    setTab(next)
    tabRefs.current[next]?.focus()
  }

  return (
    <div className="flex flex-col gap-6">
      <header className="flex items-start gap-3">
        <Avatar src={user.pictureUrl} className="size-10 text-lg" />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          {/* Wraps rather than truncating: a house name is short, and reading all of it
              matters more than a tidy single line. */}
          <h2 className="text-2xl font-semibold break-words text-brand-800">{household.name}</h2>
          {household.isOwner && (
            <div className="flex flex-wrap items-center gap-x-5">
              <span className="text-slate-600">เจ้าของบ้าน</span>
              <button type="button" onClick={() => setRenaming(true)} className={linkActionClass}>
                เปลี่ยนชื่อ
              </button>
            </div>
          )}
        </div>
      </header>

      <div
        role="tablist"
        aria-label="มุมมองของบ้าน"
        onKeyDown={onTabKeyDown}
        className="flex gap-2 rounded-xl bg-brand-100 p-2"
      >
        {TABS.map((t) => {
          const selected = t.id === tab
          return (
            <button
              key={t.id}
              id={`tab-${t.id}`}
              ref={(el) => {
                tabRefs.current[t.id] = el
              }}
              type="button"
              role="tab"
              aria-selected={selected}
              aria-controls={`panel-${t.id}`}
              // Roving tabindex: the tab strip is one stop, arrows move within it.
              tabIndex={selected ? 0 : -1}
              onClick={() => setTab(t.id)}
              className={`min-h-14 flex-1 rounded-lg px-4 font-medium ${
                selected ? 'bg-brand-700 text-white' : 'text-brand-800'
              }`}
            >
              {t.label}
            </button>
          )
        })}
      </div>

      <div role="tabpanel" id="panel-items" aria-labelledby="tab-items" hidden={tab !== 'items'}>
        <div className="flex flex-col gap-4">
          {/* Above the list, not below it. Below the list this sat off the bottom of the
              screen once a household had a dozen items. */}
          {items !== null && (
            <button type="button" onClick={() => setAdding(true)} className={`${primaryButtonClass} w-full`}>
              {items.length === 0 ? 'เพิ่มของชิ้นแรก' : '+ เพิ่มของ'}
            </button>
          )}

          {error && <Notice>{error}</Notice>}

          {items === null && <Notice tone="info">กำลังโหลดรายการ…</Notice>}

          {items?.length === 0 && (
            <section className="flex flex-col items-center gap-3 rounded-xl bg-brand-50 px-6 py-10 text-center">
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
                  onSave={async (input) => {
                    const saved = await updateItem(item.id, input)
                    // Re-sorting matters: editing a threshold can make an item low, and low
                    // items belong at the top of the list.
                    setItems((current) => sortItems((current ?? []).map((i) => (i.id === saved.id ? saved : i))))
                  }}
                  onPhoto={async (file) => {
                    const saved = await uploadItemPhoto(item.id, file)
                    setItems((current) => (current ?? []).map((i) => (i.id === saved.id ? saved : i)))
                  }}
                  onDelete={async () => {
                    await deleteItem(item.id)
                    setItems((current) => (current ?? []).filter((i) => i.id !== item.id))
                    // A deleted item's unit may no longer be in use by anything.
                    reload().catch(() => {})
                  }}
                />
              ))}
            </ul>
          )}
        </div>
      </div>

      {/* Both panels stay mounted so the tabs' aria-controls always point at something
          real, but the member list only fetches once its tab has actually been opened. */}
      <div role="tabpanel" id="panel-members" aria-labelledby="tab-members" hidden={tab !== 'members'}>
        <MembersPanel household={household} meId={user.id} active={tab === 'members'} />
      </div>

      {adding && (
        <AddItemForm
          householdId={household.id}
          units={units}
          onCancel={() => setAdding(false)}
          onAdded={(item) => {
            setAdding(false)
            setItems((current) => sortItems([...(current ?? []), item]))
            if (!units.includes(item.unit)) setUnits((u) => [...u, item.unit].sort())
          }}
        />
      )}

      {renaming && (
        <RenameHousehold
          household={household}
          onCancel={() => setRenaming(false)}
          onRenamed={(renamed) => {
            setRenaming(false)
            onRenamed(renamed)
          }}
        />
      )}
    </div>
  )
}

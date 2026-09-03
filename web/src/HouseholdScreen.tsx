import { useCallback, useEffect, useMemo, useState } from 'react'

import {
  adjustQuantity,
  deleteItem,
  getItems,
  setItemMarkedLow,
  updateItem,
  uploadItemPhoto,
  type Household,
  type Item,
  type User,
} from './api'
import AddItemForm from './AddItemForm'
import type { Tab } from './BottomNav'
import HistoryPanel from './HistoryPanel'
import ItemRow from './ItemRow'
import ScrollToTop from './ScrollToTop'
import SettingsPanel from './SettingsPanel'
import ShoppingList from './ShoppingList'
import { Avatar, inputClass, linkActionClass, Notice, primaryButtonClass, secondaryButtonClass } from './ui'

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

/**
 * Matches an item against the search box.
 *
 * A plain substring over the name and the unit, which is what someone typing "น้ำ" or
 * "ขวด" expects. No pattern semantics, so a typed % or _ finds nothing rather than
 * everything.
 */
function matches(item: Item, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (q === '') return true
  return item.name.toLowerCase().includes(q) || item.unit.toLowerCase().includes(q)
}

/**
 * The household someone lands in: its stock, its history, and its settings.
 *
 * All three panels live here rather than being split across the app, because all three
 * are about one household and two of them need the item list — the settings panel counts
 * it, and the delete confirmation refuses to be vague about what is being thrown away.
 * Which panel is showing is decided further up, by the bar at the bottom of the screen.
 */
export default function HouseholdScreen({
  household,
  households,
  user,
  tab,
  onRenamed,
  onDeleted,
  onSwitch,
  onCreateNew,
}: {
  household: Household
  /** Every household the user belongs to, offered for switching under settings. */
  households: Household[]
  user: User
  /** Which panel the bottom bar is currently pointing at. */
  tab: Tab
  onRenamed: (household: Household) => void
  onDeleted: (householdId: string) => void
  onSwitch: (householdId: string) => void
  onCreateNew: () => void
}) {
  const [items, setItems] = useState<Item[] | null>(null)
  const [units, setUnits] = useState<string[]>([])
  const [adding, setAdding] = useState(false)
  const [shopping, setShopping] = useState(false)
  const [query, setQuery] = useState('')
  const [error, setError] = useState<string | null>(null)
  // Requests in flight per item. While any are outstanding the displayed quantity is
  // optimistic, so a rapid tapper is not left waiting on the network between taps.
  const [pending, setPending] = useState<Record<string, number>>({})

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

  const anythingInFlight = Object.values(pending).some((n) => n > 0)

  /**
   * Refetches when the screen is looked at again.
   *
   * This is a household shared by several people. Someone else takes the last bottle
   * while this phone is in a pocket, and without this the number on screen is simply
   * wrong the next time it is looked at, with no way to find that out short of quitting
   * the app. Coming back into view is the moment that matters, so that is the trigger; a
   * poll would spend a suspended machine's wake-ups and a phone's data on nothing.
   *
   * Skipped while a quantity change is in flight: the number shown is deliberately
   * optimistic then, and a refetch would replace it with the value from before the tap.
   */
  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState !== 'visible' || anythingInFlight) return
      reload().catch((err: unknown) => {
        console.error('refresh items failed', err)
      })
    }
    document.addEventListener('visibilitychange', refresh)
    return () => document.removeEventListener('visibilitychange', refresh)
  }, [reload, anythingInFlight])

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

  const visible = useMemo(() => (items ?? []).filter((i) => matches(i, query)), [items, query])
  const searching = query.trim() !== ''

  /**
   * What the shopping list shows: every low item, however it became low.
   *
   * Taken from the full list rather than from `visible`, because the search box narrows
   * what is on screen and the shop still has to be told about the rest.
   */
  const lowItems = useMemo(() => (items ?? []).filter((i) => i.isLow), [items])

  return (
    <div className="flex flex-col gap-6">
      {/* Which house you are in, on every panel. The name wraps rather than truncating:
          a house name is short, and reading all of it matters more than a tidy line.
          Everything else about the household — who owns it, what it is called, who is in
          it — now lives one tap away under ตั้งค่า instead of above the daily controls. */}
      <header className="flex items-center gap-3">
        <Avatar src={user.pictureUrl} className="size-10 text-lg" />
        <h2 className="min-w-0 flex-1 text-2xl font-semibold break-words text-brand-800">{household.name}</h2>
      </header>

      <div role="tabpanel" id="panel-items" aria-labelledby="tab-items" hidden={tab !== 'items'}>
        <div className="flex flex-col gap-4">
          {/* First, and in the same place whether or not anything is low: this is what
              someone opens the app for on the way out of the door, and an errand that
              moves around is an errand that has to be hunted for. The count is on the
              button so the list does not have to be opened to learn it is empty.

              Labelled as well as drawn. An icon alone would be a guess for the person
              this app is for, and 🛒 has no accessible name of its own. */}
          {items !== null && items.length > 0 && (
            <button
              type="button"
              onClick={() => setShopping(true)}
              className={`${secondaryButtonClass} flex w-full items-center justify-center gap-3`}
            >
              <span aria-hidden="true" className="text-2xl leading-none">
                🛒
              </span>
              รายการที่ต้องซื้อ{lowItems.length > 0 && ` (${lowItems.length})`}
            </button>
          )}

          {/* Above the list, not below it. Below the list this sat off the bottom of the
              screen once a household had a dozen items. */}
          {items !== null && (
            <button type="button" onClick={() => setAdding(true)} className={`${primaryButtonClass} w-full`}>
              {items.length === 0 ? 'เพิ่มของชิ้นแรก' : '+ เพิ่มของ'}
            </button>
          )}

          {/* Filters what is already on screen rather than asking the server: the whole
              list is here, so results are instant and keep working with no signal. */}
          {items !== null && items.length > 0 && (
            <div className="flex flex-col gap-2">
              <label htmlFor="item-search" className="font-medium">
                ค้นหาของ
              </label>
              <input
                id="item-search"
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                maxLength={100}
                autoComplete="off"
                placeholder="เช่น น้ำปลา"
                className={inputClass}
              />
            </div>
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

          {/* A search that hides everything has to say so, and getting back has to be one
              tap — otherwise the list just looks empty. */}
          {items !== null && items.length > 0 && visible.length === 0 && (
            <div className="flex flex-col items-start gap-3 rounded-xl bg-brand-50 px-4 py-6">
              <p className="text-slate-600">ไม่พบของที่ค้นหา</p>
              <button type="button" onClick={() => setQuery('')} className={linkActionClass}>
                ดูทั้งหมด
              </button>
            </div>
          )}

          {searching && visible.length > 0 && (
            <p className="text-slate-600">
              พบ {visible.length} จาก {items?.length} รายการ
            </p>
          )}

          {visible.length > 0 && (
            <ul aria-label="รายการของใช้" className="flex flex-col gap-3">
              {visible.map((item) => (
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
                    // An edit can take a unit out of use or bring a new one in, and the
                    // chips offered when adding the next item come from that list.
                    reload().catch(() => {})
                  }}
                  onMarkLow={async (markedLow) => {
                    setError(null)
                    try {
                      const saved = await setItemMarkedLow(item.id, markedLow)
                      // Re-sorted for the same reason editing a threshold is: the item
                      // just became low, and low items belong at the top of the list.
                      setItems((current) => sortItems((current ?? []).map((i) => (i.id === saved.id ? saved : i))))
                    } catch (err) {
                      console.error('mark low failed', err)
                      setError('บันทึกรายการที่ต้องซื้อไม่สำเร็จ')
                    }
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

      {/* All three panels stay mounted so the bar's aria-controls always point at
          something real, but history and members only fetch once their panel is opened. */}
      <div role="tabpanel" id="panel-history" aria-labelledby="tab-history" hidden={tab !== 'history'}>
        <HistoryPanel household={household} active={tab === 'history'} />
      </div>

      <div role="tabpanel" id="panel-settings" aria-labelledby="tab-settings" hidden={tab !== 'settings'}>
        <SettingsPanel
          household={household}
          households={households}
          user={user}
          active={tab === 'settings'}
          itemCount={items?.length ?? 0}
          onRenamed={onRenamed}
          onDeleted={onDeleted}
          onSwitch={onSwitch}
          onCreateNew={onCreateNew}
        />
      </div>

      {/* Only over the stock list. It is the one panel long enough to get lost in, and a
          button that floated over a half-screen of settings would just be in the way. */}
      {tab === 'items' && <ScrollToTop />}

      {shopping && <ShoppingList items={lowItems} onClose={() => setShopping(false)} />}

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
    </div>
  )
}

import { Dialog, primaryButtonClass } from './ui'
import type { Item } from './api'

/**
 * What to buy, as one list to read in the shop.
 *
 * Deliberately not a list anyone edits. It is a view over the items that are already
 * low — by their threshold or because someone marked them by hand — so an item leaves it
 * by being restocked, never by being crossed off. That means there is no second copy of
 * the truth to go stale, and no state to reconcile when two people shop at once.
 *
 * Each line carries only what is useful standing in an aisle: what the thing is, and how
 * much is left at home. No badge, no buttons, no history — everything this list could
 * also have said is one tap away on the item itself, and none of it helps decide whether
 * to put a bottle in the basket.
 */
export default function ShoppingList({ items, onClose }: { items: Item[]; onClose: () => void }) {
  return (
    <Dialog title="รายการที่ต้องซื้อ" onClose={onClose}>
      <div className="flex flex-col gap-5">
        {items.length === 0 ? (
          <section className="flex flex-col items-center gap-3 rounded-xl bg-brand-50 px-6 py-10 text-center">
            <span aria-hidden="true" className="text-4xl">
              🛒
            </span>
            <p className="text-slate-600">ยังไม่มีของที่ต้องซื้อ</p>
          </section>
        ) : (
          <ul aria-label="รายการที่ต้องซื้อ" className="flex flex-col gap-2">
            {items.map((item) => (
              <li
                key={item.id}
                // Name and amount at opposite ends of the row rather than run together:
                // the amount is what the eye comes back to once the name has been read.
                className="flex items-baseline justify-between gap-4 rounded-xl bg-brand-50 px-4 py-3"
              >
                <span className="flex min-w-0 items-baseline gap-2">
                  <span aria-hidden="true" className="shrink-0">
                    {item.icon || '🧴'}
                  </span>
                  <span className="text-xl font-medium break-words">{item.name}</span>
                </span>
                {/* "เหลือ" is doing real work: without it a bare number beside a name
                    reads as how many to buy, which is the opposite of what it is. */}
                <span className="shrink-0 whitespace-nowrap text-slate-600">
                  เหลือ {item.quantity} {item.unit}
                </span>
              </li>
            ))}
          </ul>
        )}

        <button type="button" onClick={onClose} className={`${primaryButtonClass} w-full`}>
          ปิด
        </button>
      </div>
    </Dialog>
  )
}

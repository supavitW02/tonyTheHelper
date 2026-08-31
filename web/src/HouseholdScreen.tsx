import type { Household, User } from './api'

/**
 * The household someone lands in. For now the item list is always empty — issue #4
 * fills it. The "add the first item" control is deliberately present but disabled, so
 * the shape of the screen is real and nobody taps a button that silently does nothing.
 */
export default function HouseholdScreen({ household, user }: { household: Household; user: User }) {
  return (
    <div className="flex flex-col gap-6">
      <header className="flex items-center gap-3">
        {user.pictureUrl && (
          <img src={user.pictureUrl} alt="" className="size-10 shrink-0 rounded-full object-cover" referrerPolicy="no-referrer" />
        )}
        <div className="flex flex-col">
          <h2 className="text-2xl font-semibold">{household.name}</h2>
          {household.isOwner && <span className="text-slate-500">เจ้าของบ้าน</span>}
        </div>
      </header>

      <section aria-label="รายการของใช้" className="flex flex-col items-center gap-3 rounded-xl bg-slate-50 px-6 py-10 text-center">
        <span aria-hidden="true" className="text-4xl">
          🧺
        </span>
        <p className="text-slate-600">ยังไม่มีของในบ้านนี้</p>
      </section>

      <div className="flex flex-col gap-2">
        <button
          type="button"
          disabled
          className="min-h-14 rounded-xl bg-slate-900 px-4 py-3 font-medium text-white disabled:bg-slate-300 disabled:text-slate-500"
        >
          เพิ่มของชิ้นแรก
        </button>
        <p className="text-center text-slate-500">เพิ่มของได้เร็ว ๆ นี้</p>
      </div>
    </div>
  )
}

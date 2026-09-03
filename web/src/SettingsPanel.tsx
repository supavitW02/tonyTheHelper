import { useState } from 'react'

import type { Household, User } from './api'
import DeleteHousehold from './DeleteHousehold'
import { CheckIcon } from './icons'
import MembersPanel from './MembersPanel'
import RenameHousehold from './RenameHousehold'
import { Avatar, primaryButtonClass, secondaryButtonClass } from './ui'

/**
 * Everything about the household that is not its stock.
 *
 * These are the rare errands — who you are signed in as, what this house is called, who
 * is in it, which house you are looking at, and making another one. They used to be two
 * buttons and a dropdown floating above the daily screen, where they cost every user a
 * row of chrome on every visit to pay for something most of them do once. Now they are
 * one tap away and nowhere near the plus and minus buttons.
 *
 * Read top to bottom it answers, in order: who am I, where am I, who else is here, where
 * else could I be. Deleting the house is last, because it is the one thing here that
 * cannot be undone.
 */
export default function SettingsPanel({
  household,
  households,
  user,
  active,
  itemCount,
  onRenamed,
  onDeleted,
  onSwitch,
  onCreateNew,
}: {
  household: Household
  /** Every household the user belongs to, so they can move between them. */
  households: Household[]
  user: User
  /** Whether this panel is the one being looked at. */
  active: boolean
  /** Shown as part of the household's detail, and used by the delete confirmation. */
  itemCount: number
  onRenamed: (household: Household) => void
  onDeleted: (householdId: string) => void
  onSwitch: (householdId: string) => void
  onCreateNew: () => void
}) {
  const [renaming, setRenaming] = useState(false)

  return (
    <div className="flex flex-col gap-8">
      <section className="flex items-center gap-3 rounded-xl bg-brand-50 p-4">
        <Avatar src={user.pictureUrl} className="size-14 text-2xl" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="break-words font-medium">{user.displayName}</span>
          <span className="text-slate-600">เข้าสู่ระบบด้วย LINE</span>
        </div>
      </section>

      <section className="flex flex-col gap-3">
        <h3 className="text-xl font-semibold text-brand-800">บ้านนี้</h3>

        {/* A list of facts rather than a paragraph: each one is short, and someone
            checking "am I in the right house" wants to find it, not read it. */}
        <dl className="flex flex-col gap-3 rounded-xl border-2 border-brand-200 p-4">
          <div className="flex flex-wrap items-baseline justify-between gap-x-5 gap-y-1">
            <dt className="text-slate-600">ชื่อบ้าน</dt>
            <dd className="font-medium break-words">{household.name}</dd>
          </div>
          <div className="flex flex-wrap items-baseline justify-between gap-x-5 gap-y-1">
            <dt className="text-slate-600">สถานะของคุณ</dt>
            <dd className="font-medium">{household.isOwner ? 'เจ้าของบ้าน' : 'สมาชิก'}</dd>
          </div>
          <div className="flex flex-wrap items-baseline justify-between gap-x-5 gap-y-1">
            <dt className="text-slate-600">ของทั้งหมด</dt>
            <dd className="font-medium">{itemCount} รายการ</dd>
          </div>
        </dl>

        {/* Only the owner may rename, and the API enforces it either way. */}
        {household.isOwner && (
          <button type="button" onClick={() => setRenaming(true)} className={secondaryButtonClass}>
            เปลี่ยนชื่อบ้าน
          </button>
        )}
      </section>

      <section className="flex flex-col gap-3">
        <h3 className="text-xl font-semibold text-brand-800">สมาชิก</h3>
        <MembersPanel household={household} meId={user.id} active={active} />
      </section>

      <section className="flex flex-col gap-3">
        <h3 className="text-xl font-semibold text-brand-800">บ้านของฉัน</h3>

        {/* A row of tall buttons rather than the <select> this replaced. A native picker
            on a phone is a tap, a scroll wheel, and a confirm to change one word; these
            are one tap each, and the one you are already in is visible without opening
            anything. Belonging to a single house still lists it, because the list is
            also the answer to "which house am I in". */}
        <ul aria-label="บ้านที่คุณอยู่" className="flex flex-col gap-3">
          {households.map((h) => {
            const current = h.id === household.id
            return (
              <li key={h.id}>
                <button
                  type="button"
                  aria-current={current ? 'true' : undefined}
                  onClick={() => {
                    if (!current) onSwitch(h.id)
                  }}
                  className={`flex min-h-14 w-full items-center gap-3 rounded-xl border-2 px-4 py-3 text-left ${
                    current
                      ? 'border-brand-700 bg-brand-100 font-semibold text-brand-900'
                      : 'border-brand-200 text-brand-950'
                  }`}
                >
                  <span className="min-w-0 flex-1 break-words">{h.name}</span>
                  {current && (
                    <span className="flex shrink-0 items-center gap-2 whitespace-nowrap text-brand-800">
                      <CheckIcon className="size-5" />
                      กำลังใช้อยู่
                    </span>
                  )}
                </button>
              </li>
            )
          })}
        </ul>

        <button type="button" onClick={onCreateNew} className={primaryButtonClass}>
          + สร้างบ้านใหม่
        </button>
      </section>

      {/* Owner-only, and last. Rendered here rather than inside MembersPanel because the
          confirmation needs the real item count, and that list is not the member panel's
          business. */}
      {household.isOwner && (
        <DeleteHousehold household={household} itemCount={itemCount} onDeleted={onDeleted} />
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

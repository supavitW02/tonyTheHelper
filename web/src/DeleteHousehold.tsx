import { useRef, useState } from 'react'

import { ApiError, deleteHousehold, type Household } from './api'
import { dangerButtonClass, Dialog, Notice, secondaryButtonClass } from './ui'

/**
 * Deleting a household, for its owner.
 *
 * Deliberately the last thing on the members tab rather than anywhere near the daily
 * controls: it is rare, it is irreversible from inside the app, and it takes the household
 * away from everyone in it, not just the person tapping.
 *
 * The confirmation says what will actually be lost, with the real item count in it. A
 * dialog that only says "are you sure?" gives someone nothing to be sure about.
 */
export default function DeleteHousehold({
  household,
  itemCount,
  onDeleted,
}: {
  household: Household
  itemCount: number
  onDeleted: (householdId: string) => void
}) {
  const [confirming, setConfirming] = useState(false)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function remove() {
    setBusy(true)
    setError(null)
    try {
      await deleteHousehold(household.id)
      onDeleted(household.id)
    } catch (err) {
      console.error('delete household failed', err)
      setError(
        err instanceof ApiError && err.status === 403
          ? 'เฉพาะเจ้าของบ้านเท่านั้นที่ลบบ้านได้'
          : 'ลบบ้านไม่สำเร็จ กรุณาลองใหม่อีกครั้ง',
      )
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-3 border-t-2 border-brand-100 pt-6">
      <h3 className="text-xl font-semibold text-brand-800">ลบบ้านนี้</h3>
      <p className="text-slate-600">ทุกคนในบ้านจะไม่เห็นบ้านนี้และของทั้งหมดอีก</p>

      <button
        type="button"
        onClick={() => setConfirming(true)}
        className="-mx-2 min-h-12 self-start px-2 text-red-700 underline underline-offset-4"
      >
        ลบบ้าน “{household.name}”
      </button>

      {confirming && (
        <Dialog
          title={`ลบบ้าน “${household.name}”?`}
          onClose={() => setConfirming(false)}
          initialFocus={cancelRef}
        >
          <div className="flex flex-col gap-5">
            <Notice>
              ของ {itemCount} รายการ พร้อมประวัติทั้งหมด จะไม่แสดงอีก และสมาชิกทุกคนจะไม่เห็นบ้านนี้
            </Notice>
            <p className="text-slate-600">ทำแล้วย้อนกลับไม่ได้จากในแอป</p>

            {error && <Notice>{error}</Notice>}

            <button type="button" disabled={busy} onClick={remove} className={dangerButtonClass}>
              {busy ? 'กำลังลบ…' : 'ยืนยันลบบ้าน'}
            </button>
            {/* Focus lands on the way out, not the way through: showModal() otherwise
                focuses the first focusable element — the destructive one — so a stray
                Enter would delete the household. The confirm button stays first
                visually, because that is the order the rest of the app confirms in. */}
            <button
              ref={cancelRef}
              type="button"
              disabled={busy}
              onClick={() => setConfirming(false)}
              className={secondaryButtonClass}
            >
              ไม่ลบ
            </button>
          </div>
        </Dialog>
      )}
    </section>
  )
}

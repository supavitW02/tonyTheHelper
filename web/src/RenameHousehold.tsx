import { useState } from 'react'

import { ApiError, renameHousehold, type Household } from './api'
import { Dialog, Field, inputClass, Notice, primaryButtonClass, secondaryButtonClass } from './ui'

/**
 * Renaming a household, for its owner.
 *
 * A house gets named once, in a hurry, on the day it is created — "บ้าน" or a typo. The
 * name then shows at the top of every screen and in the switcher, so being stuck with it
 * is a small daily annoyance. Only the owner may change it; the API enforces that, and
 * the control is not rendered for anyone else.
 */
export default function RenameHousehold({
  household,
  onRenamed,
  onCancel,
}: {
  household: Household
  onRenamed: (household: Household) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(household.name)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const trimmed = name.trim()
  const ready = trimmed !== '' && trimmed !== household.name

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (!ready || saving) return

    setSaving(true)
    setError(null)
    try {
      onRenamed(await renameHousehold(household.id, trimmed))
    } catch (err) {
      console.error('rename household failed', err)
      // 403 means someone else took ownership, or this tab is out of date. Saying "try
      // again" would be a lie, so it says who can do it instead.
      setError(
        err instanceof ApiError && err.status === 403
          ? 'เฉพาะเจ้าของบ้านเท่านั้นที่เปลี่ยนชื่อบ้านได้'
          : 'เปลี่ยนชื่อบ้านไม่สำเร็จ กรุณาลองใหม่อีกครั้ง',
      )
      setSaving(false)
    }
  }

  return (
    <Dialog title="เปลี่ยนชื่อบ้าน" onClose={onCancel}>
      <form onSubmit={submit} className="flex flex-col gap-5">
        <Field label="ชื่อบ้าน" htmlFor="rename-household">
          <input
            id="rename-household"
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={60}
            autoComplete="off"
            placeholder="เช่น บ้านเรา"
            className={inputClass}
          />
        </Field>

        <p className="text-slate-600">ทุกคนในบ้านจะเห็นชื่อใหม่นี้</p>

        {error && <Notice>{error}</Notice>}

        <div className="flex flex-col gap-3">
          <button type="submit" disabled={!ready || saving} className={primaryButtonClass}>
            {saving ? 'กำลังบันทึก…' : 'บันทึกชื่อใหม่'}
          </button>
          <button type="button" onClick={onCancel} disabled={saving} className={secondaryButtonClass}>
            ยกเลิก
          </button>
        </div>
      </form>
    </Dialog>
  )
}

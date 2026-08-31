import { useState } from 'react'

import { createHousehold, type Household } from './api'
import { Field, inputClass, Notice, primaryButtonClass, secondaryButtonClass } from './ui'

/**
 * Shown to someone who belongs to no household yet. It is the first thing a new user
 * sees after signing in, so it explains what a household is rather than just asking
 * for a name — and it is the one screen with room for the logo.
 */
export default function CreateHousehold({
  onCreated,
  onCancel,
}: {
  onCreated: (h: Household) => void
  /** Absent for the very first household, where there is nothing to go back to. */
  onCancel?: () => void
}) {
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const trimmed = name.trim()

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (!trimmed || saving) return

    setSaving(true)
    setError(null)
    try {
      onCreated(await createHousehold(trimmed))
    } catch (err) {
      console.error('create household failed', err)
      setError('สร้างบ้านไม่สำเร็จ กรุณาลองใหม่อีกครั้ง')
      setSaving(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-6">
      <div className="flex flex-col items-center gap-3 text-center">
        <img src="/tony-logo.png" alt="" width={192} height={192} className="size-48" />
        <h2 className="text-2xl font-semibold text-brand-800">สร้างบ้านของคุณ</h2>
        <p className="text-slate-600">บ้านคือที่เก็บรายการของใช้ที่ทุกคนในครอบครัวใช้ร่วมกัน</p>
      </div>

      <Field label="ชื่อบ้าน" htmlFor="household-name">
        <input
          id="household-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={60}
          autoComplete="off"
          placeholder="เช่น บ้านเรา"
          className={inputClass}
        />
      </Field>

      {error && <Notice>{error}</Notice>}

      <button type="submit" disabled={!trimmed || saving} className={primaryButtonClass}>
        {saving ? 'กำลังสร้าง…' : 'สร้างบ้าน'}
      </button>

      {onCancel && (
        <button type="button" onClick={onCancel} disabled={saving} className={secondaryButtonClass}>
          ยกเลิก
        </button>
      )}
    </form>
  )
}

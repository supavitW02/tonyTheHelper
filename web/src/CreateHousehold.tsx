import { useState } from 'react'

import { createHousehold, type Household } from './api'

/**
 * Shown to someone who belongs to no household yet. It is the first thing a new user
 * sees after signing in, so it explains what a household is rather than just asking
 * for a name.
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
      <div className="flex flex-col gap-2">
        <h2 className="text-2xl font-semibold">สร้างบ้านของคุณ</h2>
        <p className="text-slate-600">บ้านคือที่เก็บรายการของใช้ที่ทุกคนในครอบครัวใช้ร่วมกัน</p>
      </div>

      <div className="flex flex-col gap-2">
        <label htmlFor="household-name" className="font-medium">
          ชื่อบ้าน
        </label>
        <input
          id="household-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={60}
          autoComplete="off"
          placeholder="เช่น บ้านเรา"
          className="min-h-14 rounded-xl border-2 border-slate-300 px-4 py-3 focus:border-slate-900 focus:outline-none"
        />
      </div>

      {error && (
        <p aria-live="assertive" className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900">
          <span aria-hidden="true" className="text-2xl leading-none">
            ✕
          </span>
          <span>{error}</span>
        </p>
      )}

      <button
        type="submit"
        disabled={!trimmed || saving}
        className="min-h-14 rounded-xl bg-slate-900 px-4 py-3 font-medium text-white disabled:bg-slate-300 disabled:text-slate-500"
      >
        {saving ? 'กำลังสร้าง…' : 'สร้างบ้าน'}
      </button>

      {onCancel && (
        <button type="button" onClick={onCancel} disabled={saving} className="min-h-14 rounded-xl border-2 border-slate-300 px-4 py-3">
          ยกเลิก
        </button>
      )}
    </form>
  )
}

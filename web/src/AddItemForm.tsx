import { useState } from 'react'

import { createItem, type Item } from './api'

/**
 * Adding an item. The unit field stays free text — people name units however they like —
 * but the household's existing units appear as tappable chips so the same unit does not
 * end up with three spellings.
 */
export default function AddItemForm({
  householdId,
  units,
  onAdded,
  onCancel,
}: {
  householdId: string
  units: string[]
  onAdded: (item: Item) => void
  onCancel: () => void
}) {
  const [name, setName] = useState('')
  const [unit, setUnit] = useState('')
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const ready = name.trim() !== '' && unit.trim() !== ''

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (!ready || saving) return

    setSaving(true)
    setError(null)
    try {
      onAdded(
        await createItem(householdId, {
          name: name.trim(),
          unit: unit.trim(),
          description: description.trim() || undefined,
        }),
      )
    } catch (err) {
      console.error('create item failed', err)
      setError('เพิ่มของไม่สำเร็จ กรุณาลองใหม่อีกครั้ง')
      setSaving(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-5 rounded-xl bg-slate-50 p-5">
      <h3 className="text-xl font-semibold">เพิ่มของ</h3>

      <Field label="ชื่อของ" htmlFor="item-name">
        <input
          id="item-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={100}
          autoComplete="off"
          placeholder="เช่น น้ำปลา"
          className={inputClass}
        />
      </Field>

      <Field label="หน่วยนับ" htmlFor="item-unit">
        <input
          id="item-unit"
          type="text"
          value={unit}
          onChange={(e) => setUnit(e.target.value)}
          maxLength={30}
          autoComplete="off"
          placeholder="เช่น ขวด"
          className={inputClass}
        />
        {units.length > 0 && (
          <div className="flex flex-wrap gap-2 pt-1">
            {units.map((u) => (
              <button
                key={u}
                type="button"
                onClick={() => setUnit(u)}
                aria-pressed={unit === u}
                className={`min-h-12 rounded-full border-2 px-4 ${
                  unit === u ? 'border-slate-900 bg-slate-900 text-white' : 'border-slate-300 bg-white'
                }`}
              >
                {u}
              </button>
            ))}
          </div>
        )}
      </Field>

      <Field label="รายละเอียด (ไม่บังคับ)" htmlFor="item-description">
        <input
          id="item-description"
          type="text"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          maxLength={500}
          autoComplete="off"
          className={inputClass}
        />
      </Field>

      {error && (
        <p aria-live="assertive" className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900">
          <span aria-hidden="true" className="text-2xl leading-none">
            ✕
          </span>
          <span>{error}</span>
        </p>
      )}

      <div className="flex flex-col gap-3">
        <button
          type="submit"
          disabled={!ready || saving}
          className="min-h-14 rounded-xl bg-slate-900 px-4 py-3 font-medium text-white disabled:bg-slate-300 disabled:text-slate-500"
        >
          {saving ? 'กำลังบันทึก…' : 'บันทึก'}
        </button>
        <button type="button" onClick={onCancel} className="min-h-14 rounded-xl border-2 border-slate-300 px-4 py-3">
          ยกเลิก
        </button>
      </div>
    </form>
  )
}

const inputClass =
  'min-h-14 w-full rounded-xl border-2 border-slate-300 px-4 py-3 focus:border-slate-900 focus:outline-none'

function Field({ label, htmlFor, children }: { label: string; htmlFor: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={htmlFor} className="font-medium">
        {label}
      </label>
      {children}
    </div>
  )
}

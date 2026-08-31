import { useState } from 'react'

import { createItem, type Item } from './api'
import { Dialog, Field, inputClass, Notice, primaryButtonClass, secondaryButtonClass } from './ui'

/**
 * Adding an item, in an overlay rather than a form below the list.
 *
 * Below the list it was easy to miss: on a phone the household's items already fill the
 * screen, so the form appeared off-screen and nothing about the page said it was there.
 * As a dialog it arrives in front of the user, takes the focus, and cannot be scrolled
 * away from by accident.
 *
 * The unit field stays free text — people name units however they like — but the
 * household's existing units appear as tappable chips so the same unit does not end up
 * with three spellings.
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
  const [threshold, setThreshold] = useState('')
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
          // Empty means "never warn me", which the API expects as null.
          lowStockThreshold: threshold.trim() === '' ? null : Number(threshold),
        }),
      )
    } catch (err) {
      console.error('create item failed', err)
      setError('เพิ่มของไม่สำเร็จ กรุณาลองใหม่อีกครั้ง')
      setSaving(false)
    }
  }

  return (
    <Dialog title="เพิ่มของ" onClose={onCancel}>
      <form onSubmit={submit} className="flex flex-col gap-5">
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
                    unit === u ? 'border-brand-700 bg-brand-700 text-white' : 'border-brand-600 bg-white'
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

        <Field label="เตือนเมื่อเหลือน้อยกว่าหรือเท่ากับ (ไม่บังคับ)" htmlFor="item-threshold">
          <input
            id="item-threshold"
            type="number"
            inputMode="numeric"
            min={0}
            value={threshold}
            onChange={(e) => setThreshold(e.target.value)}
            placeholder="ไม่เตือน"
            className={inputClass}
          />
        </Field>

        {error && <Notice>{error}</Notice>}

        <div className="flex flex-col gap-3">
          <button type="submit" disabled={!ready || saving} className={primaryButtonClass}>
            {saving ? 'กำลังบันทึก…' : 'บันทึก'}
          </button>
          <button type="button" onClick={onCancel} disabled={saving} className={secondaryButtonClass}>
            ยกเลิก
          </button>
        </div>
      </form>
    </Dialog>
  )
}

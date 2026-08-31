import { useEffect, useState } from 'react'

import { getMovements, itemImageUrl, type Item, type ItemInput, type Movement } from './api'

type Panel = null | 'total' | 'history' | 'edit' | 'photo'

/**
 * One item in the household list.
 *
 * Laid out in stacked rows rather than one horizontal strip. At the app's 125% root font
 * size a phone gives about 340px of content width, and a thumbnail plus two 56px buttons
 * plus the name does not fit on one line — the name truncated and the "ใกล้หมด" badge
 * collided with the minus button. Each row now owns its full width.
 */
export default function ItemRow({
  item,
  onStep,
  onSetTotal,
  onSave,
  onDelete,
  onPhoto,
}: {
  item: Item
  onStep: (delta: number) => void
  onSetTotal: (quantity: number, note: string) => void
  onSave: (input: ItemInput) => Promise<void>
  onDelete: () => Promise<void>
  onPhoto: (file: File) => Promise<void>
}) {
  const [panel, setPanel] = useState<Panel>(null)
  const toggle = (next: Exclude<Panel, null>) => setPanel((open) => (open === next ? null : next))

  return (
    <li className="flex flex-col gap-4 rounded-xl border-2 border-slate-200 p-4">
      <div className="flex items-start gap-3">
        <Thumbnail item={item} />
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          {/* Wraps rather than truncating: reading the whole name matters more than
              keeping every row the same height. */}
          <span className="text-xl font-medium break-words">{item.name}</span>
          {item.isLow && (
            <span className="flex w-fit items-center gap-1 rounded-full bg-amber-100 px-3 py-1 text-amber-900">
              <span aria-hidden="true">⚠</span> ใกล้หมด
            </span>
          )}
        </div>
      </div>

      {/* The everyday action gets a row to itself, so both buttons keep their full size
          and the quantity has room to breathe. */}
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => toggle('total')}
          aria-expanded={panel === 'total'}
          className="min-h-14 flex-1 rounded-xl bg-slate-50 px-4 text-left text-slate-700"
        >
          {item.quantity} {item.unit}
        </button>
        <StepButton label={`ลด ${item.name}`} disabled={item.quantity === 0} onClick={() => onStep(-1)}>
          −
        </StepButton>
        <StepButton label={`เพิ่ม ${item.name}`} disabled={false} onClick={() => onStep(1)}>
          +
        </StepButton>
      </div>

      <div className="flex flex-wrap gap-x-6 gap-y-1">
        <SmallAction onClick={() => toggle('history')} expanded={panel === 'history'}>
          ประวัติ
        </SmallAction>
        {/* Its own action rather than buried inside แก้ไข, where nobody found it. */}
        <SmallAction onClick={() => toggle('photo')} expanded={panel === 'photo'}>
          รูปภาพ
        </SmallAction>
        <SmallAction onClick={() => toggle('edit')} expanded={panel === 'edit'}>
          แก้ไข
        </SmallAction>
      </div>

      {panel === 'total' && (
        <SetTotalForm
          item={item}
          onCancel={() => setPanel(null)}
          onSubmit={(quantity, note) => {
            setPanel(null)
            onSetTotal(quantity, note)
          }}
        />
      )}

      {panel === 'history' && <History itemId={item.id} unit={item.unit} />}

      {panel === 'photo' && (
        <Panelled>
          <PhotoPicker item={item} onPhoto={onPhoto} />
        </Panelled>
      )}

      {panel === 'edit' && (
        <EditForm
          item={item}
          onCancel={() => setPanel(null)}
          onSave={async (input) => {
            await onSave(input)
            setPanel(null)
          }}
          onDelete={onDelete}
        />
      )}
    </li>
  )
}

/** A separator above an expanded panel, so it reads as belonging to this item. */
function Panelled({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-col gap-3 border-t-2 border-slate-100 pt-4">{children}</div>
}

/**
 * The item's photo, or a neutral stand-in. An item without one must look deliberate
 * rather than broken, since most items will never have a photo.
 */
function Thumbnail({ item }: { item: Item }) {
  if (!item.hasImage) {
    return (
      <span
        aria-hidden="true"
        className="flex size-14 shrink-0 items-center justify-center rounded-xl bg-slate-100 text-2xl"
      >
        🧴
      </span>
    )
  }
  return (
    <img
      src={itemImageUrl(item.id)}
      alt={item.name}
      loading="lazy"
      className="size-14 shrink-0 rounded-xl bg-slate-100 object-cover"
    />
  )
}

function StepButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string
  disabled: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      // size-14 is 3.5rem, and rem is 20px here, so these render at 70px square —
      // well above the tap-target floor and easy to hit one-handed.
      className="size-14 shrink-0 rounded-xl bg-slate-900 text-3xl leading-none text-white disabled:bg-slate-200 disabled:text-slate-400"
    >
      {children}
    </button>
  )
}

function SmallAction({
  onClick,
  expanded,
  children,
}: {
  onClick: () => void
  expanded: boolean
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-expanded={expanded}
      className={`min-h-12 text-slate-600 underline underline-offset-4 ${expanded ? 'font-medium text-slate-900' : ''}`}
    >
      {children}
    </button>
  )
}

const inputClass =
  'min-h-14 w-full rounded-xl border-2 border-slate-300 px-4 py-3 focus:border-slate-900 focus:outline-none'

function SetTotalForm({
  item,
  onSubmit,
  onCancel,
}: {
  item: Item
  onSubmit: (quantity: number, note: string) => void
  onCancel: () => void
}) {
  const [value, setValue] = useState(String(item.quantity))
  const [note, setNote] = useState('')

  const quantity = Number(value)
  const valid = value.trim() !== '' && Number.isInteger(quantity) && quantity >= 0

  return (
    <Panelled>
      <label htmlFor={`qty-${item.id}`} className="font-medium">
        แก้ไขจำนวน
      </label>
      <input
        id={`qty-${item.id}`}
        type="number"
        inputMode="numeric"
        min={0}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        className={inputClass}
      />
      <input
        type="text"
        value={note}
        onChange={(e) => setNote(e.target.value)}
        maxLength={300}
        placeholder="หมายเหตุ (ไม่บังคับ)"
        className={inputClass}
      />
      <button
        type="button"
        disabled={!valid}
        onClick={() => onSubmit(quantity, note.trim())}
        className="min-h-14 rounded-xl bg-slate-900 px-4 font-medium text-white disabled:bg-slate-300 disabled:text-slate-500"
      >
        บันทึก
      </button>
      <button type="button" onClick={onCancel} className="min-h-14 rounded-xl border-2 border-slate-300 px-4">
        ยกเลิก
      </button>
    </Panelled>
  )
}

/** The question a shared household actually asks: who used the last one, and when. */
function History({ itemId, unit }: { itemId: string; unit: string }) {
  const [movements, setMovements] = useState<Movement[] | null>(null)
  const [failed, setFailed] = useState(false)

  // Fetched in an effect, not during render: the history loads lazily when the panel
  // opens, and fetching in the render path would re-fire on every re-render.
  useEffect(() => {
    let cancelled = false

    getMovements(itemId)
      .then(({ movements }) => {
        if (!cancelled) setMovements(movements)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        console.error('load history failed', err)
        setFailed(true)
      })

    return () => {
      cancelled = true
    }
  }, [itemId])

  return (
    <Panelled>
      {failed && <p className="text-amber-900">โหลดประวัติไม่สำเร็จ</p>}
      {!failed && movements === null && <p className="text-slate-600">กำลังโหลดประวัติ…</p>}
      {movements?.length === 0 && <p className="text-slate-600">ยังไม่มีการเปลี่ยนแปลง</p>}
      {movements && movements.length > 0 && (
        <ul className="flex flex-col gap-3">
          {movements.map((m) => (
            <li key={m.id} className="flex flex-col">
              <span>
                <strong className="font-medium">{m.actorName}</strong> {m.quantityBefore} → {m.quantityAfter} {unit}
              </span>
              <span className="text-slate-600">
                {new Date(m.createdAt).toLocaleString('th-TH', { dateStyle: 'medium', timeStyle: 'short' })}
              </span>
              {m.note && <span className="text-slate-600">“{m.note}”</span>}
            </li>
          ))}
        </ul>
      )}
    </Panelled>
  )
}

/** Choosing a photo. accept="image/*" is what offers the camera or the gallery on a phone. */
function PhotoPicker({ item, onPhoto }: { item: Item; onPhoto: (file: File) => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)

  return (
    <>
      <label htmlFor={`photo-${item.id}`} className="font-medium">
        {item.hasImage ? 'เปลี่ยนรูปภาพ' : 'เพิ่มรูปภาพ'}
      </label>
      <input
        id={`photo-${item.id}`}
        type="file"
        accept="image/*"
        disabled={busy}
        onChange={async (e) => {
          const file = e.target.files?.[0]
          if (!file) return
          setBusy(true)
          setFailed(false)
          try {
            await onPhoto(file)
          } catch (err) {
            console.error('photo upload failed', err)
            setFailed(true)
          } finally {
            setBusy(false)
            // Clear the input so picking the same file again still fires a change.
            e.target.value = ''
          }
        }}
        className="w-full rounded-xl border-2 border-slate-300 p-3"
      />
      {busy && <p aria-live="polite" className="text-slate-600">กำลังอัปโหลดรูป…</p>}
      {failed && <p aria-live="assertive" className="text-amber-900">อัปโหลดรูปไม่สำเร็จ กรุณาลองใหม่</p>}
    </>
  )
}

function EditForm({
  item,
  onSave,
  onDelete,
  onCancel,
}: {
  item: Item
  onSave: (input: ItemInput) => Promise<void>
  onDelete: () => Promise<void>
  onCancel: () => void
}) {
  const [name, setName] = useState(item.name)
  const [unit, setUnit] = useState(item.unit)
  const [description, setDescription] = useState(item.description ?? '')
  const [threshold, setThreshold] = useState(item.lowStockThreshold?.toString() ?? '')
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)

  const ready = name.trim() !== '' && unit.trim() !== '' && !busy

  return (
    <Panelled>
      <label htmlFor={`name-${item.id}`} className="font-medium">
        ชื่อของ
      </label>
      <input id={`name-${item.id}`} type="text" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} className={inputClass} />

      <label htmlFor={`unit-${item.id}`} className="font-medium">
        หน่วยนับ
      </label>
      <input id={`unit-${item.id}`} type="text" value={unit} maxLength={30} onChange={(e) => setUnit(e.target.value)} className={inputClass} />

      <label htmlFor={`desc-${item.id}`} className="font-medium">
        รายละเอียด (ไม่บังคับ)
      </label>
      <input id={`desc-${item.id}`} type="text" value={description} maxLength={500} onChange={(e) => setDescription(e.target.value)} className={inputClass} />

      <label htmlFor={`low-${item.id}`} className="font-medium">
        เตือนเมื่อเหลือน้อยกว่าหรือเท่ากับ (ไม่บังคับ)
      </label>
      <input
        id={`low-${item.id}`}
        type="number"
        inputMode="numeric"
        min={0}
        value={threshold}
        placeholder="ไม่เตือน"
        onChange={(e) => setThreshold(e.target.value)}
        className={inputClass}
      />

      <button
        type="button"
        disabled={!ready}
        onClick={async () => {
          setBusy(true)
          try {
            await onSave({
              name: name.trim(),
              unit: unit.trim(),
              description: description.trim() || undefined,
              // An empty field means "never warn me", which the API expects as null.
              lowStockThreshold: threshold.trim() === '' ? null : Number(threshold),
            })
          } finally {
            setBusy(false)
          }
        }}
        className="min-h-14 rounded-xl bg-slate-900 px-4 font-medium text-white disabled:bg-slate-300 disabled:text-slate-500"
      >
        บันทึก
      </button>
      <button type="button" onClick={onCancel} className="min-h-14 rounded-xl border-2 border-slate-300 px-4">
        ยกเลิก
      </button>

      {/* Two steps rather than one, because deleting is the one action here that a
          mis-tap should not be able to complete. */}
      {confirming ? (
        <div className="flex flex-col gap-3 rounded-xl bg-amber-50 p-4">
          <p className="text-amber-900">ลบ “{item.name}” ออกจากรายการ? ประวัติจะยังเก็บไว้</p>
          <button
            type="button"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              try {
                await onDelete()
              } finally {
                setBusy(false)
              }
            }}
            className="min-h-14 rounded-xl bg-red-700 px-4 font-medium text-white disabled:bg-slate-300"
          >
            ยืนยันลบ
          </button>
          <button type="button" onClick={() => setConfirming(false)} className="min-h-14 rounded-xl border-2 border-slate-300 px-4">
            ไม่ลบ
          </button>
        </div>
      ) : (
        <button type="button" onClick={() => setConfirming(true)} className="min-h-14 text-red-700 underline underline-offset-4">
          ลบของนี้
        </button>
      )}
    </Panelled>
  )
}

import { useState } from 'react'

import type { Item } from './api'

/**
 * One item in the household list.
 *
 * The everyday action is "we used one", so minus and plus are large, always visible, and
 * need no keyboard. Setting a total — after a shopping trip, when twelve taps would be
 * cruel — is one tap further in, behind the quantity itself.
 */
export default function ItemRow({
  item,
  onStep,
  onSetTotal,
}: {
  item: Item
  onStep: (delta: number) => void
  onSetTotal: (quantity: number, note: string) => void
}) {
  const [editing, setEditing] = useState(false)

  return (
    <li className="flex flex-col gap-3 rounded-xl border-2 border-slate-200 p-4">
      <div className="flex items-center gap-4">
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-xl font-medium">{item.name}</span>
          <button
            type="button"
            onClick={() => setEditing((open) => !open)}
            aria-expanded={editing}
            className="self-start text-slate-600 underline underline-offset-4"
          >
            {item.quantity} {item.unit}
          </button>
        </div>

        <StepButton label={`ลด ${item.name}`} disabled={item.quantity === 0} onClick={() => onStep(-1)}>
          −
        </StepButton>
        <StepButton label={`เพิ่ม ${item.name}`} disabled={false} onClick={() => onStep(1)}>
          +
        </StepButton>
      </div>

      {editing && (
        <SetTotalForm
          item={item}
          onCancel={() => setEditing(false)}
          onSubmit={(quantity, note) => {
            setEditing(false)
            onSetTotal(quantity, note)
          }}
        />
      )}
    </li>
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
      // 56px square: comfortably above the tap-target floor and reachable one-handed.
      className="size-14 shrink-0 rounded-xl bg-slate-900 text-3xl leading-none text-white disabled:bg-slate-200 disabled:text-slate-400"
    >
      {children}
    </button>
  )
}

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
    <div className="flex flex-col gap-3 border-t-2 border-slate-100 pt-3">
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
        className="min-h-14 rounded-xl border-2 border-slate-300 px-4 py-3 focus:border-slate-900 focus:outline-none"
      />
      <input
        type="text"
        value={note}
        onChange={(e) => setNote(e.target.value)}
        maxLength={300}
        placeholder="หมายเหตุ (ไม่บังคับ)"
        className="min-h-14 rounded-xl border-2 border-slate-300 px-4 py-3 focus:border-slate-900 focus:outline-none"
      />
      <div className="flex gap-3">
        <button
          type="button"
          disabled={!valid}
          onClick={() => onSubmit(quantity, note.trim())}
          className="min-h-14 flex-1 rounded-xl bg-slate-900 px-4 font-medium text-white disabled:bg-slate-300 disabled:text-slate-500"
        >
          บันทึก
        </button>
        <button type="button" onClick={onCancel} className="min-h-14 flex-1 rounded-xl border-2 border-slate-300 px-4">
          ยกเลิก
        </button>
      </div>
    </div>
  )
}

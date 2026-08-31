import { useId } from 'react'

/**
 * The icons on offer, with a Thai name for each.
 *
 * The label is not decoration: it is the button's accessible name, so the picker is
 * usable by someone who cannot make out a 30px emoji, and it is what a screen reader
 * reads out. Chosen for what a Thai household actually keeps and runs out of, in rough
 * order of how often that is — kitchen staples, then cleaning, then bathroom, then the
 * things you only think about when they fail.
 *
 * This list lives here rather than in the API on purpose. The server checks that an icon
 * is a symbol and not a label, but which symbols are offered is a question about this
 * picker, so adding one is a frontend change and needs no deploy of the API.
 */
const ICONS: { icon: string; label: string }[] = [
  { icon: '🍚', label: 'ข้าวสาร' },
  { icon: '🍶', label: 'น้ำปลา ซอส' },
  { icon: '🧂', label: 'เกลือ เครื่องปรุง' },
  { icon: '🥛', label: 'นม' },
  { icon: '🍜', label: 'บะหมี่ เส้น' },
  { icon: '🥚', label: 'ไข่' },
  { icon: '🍞', label: 'ขนมปัง' },
  { icon: '☕', label: 'กาแฟ ชา' },
  { icon: '🧃', label: 'น้ำผลไม้' },
  { icon: '🧊', label: 'น้ำแข็ง' },
  { icon: '🫧', label: 'น้ำยาล้างจาน' },
  { icon: '🧴', label: 'น้ำยา แบบขวดปั๊ม' },
  { icon: '🧼', label: 'สบู่' },
  { icon: '🧻', label: 'กระดาษชำระ' },
  { icon: '🪥', label: 'แปรงฟัน ยาสีฟัน' },
  { icon: '🧹', label: 'ไม้กวาด' },
  { icon: '🧺', label: 'ตะกร้า ผ้า' },
  { icon: '🪣', label: 'ถังน้ำ' },
  { icon: '💊', label: 'ยา' },
  { icon: '🩹', label: 'พลาสเตอร์ยา' },
  { icon: '🔋', label: 'ถ่านไฟฉาย' },
  { icon: '💡', label: 'หลอดไฟ' },
]

/**
 * Choosing a picture for an item without using the camera.
 *
 * Photographing every bottle is real work for the person this app is for: find the item,
 * hold the phone steady, wait for an upload over mobile data. An icon is one tap, and a
 * row with a symbol on it is far quicker to scan than a row without.
 *
 * Tapping the chosen icon again clears it, and there is an explicit way out below as
 * well — a picker you cannot change your mind in is worse than no picker.
 */
export default function IconPicker({ value, onChange }: { value: string; onChange: (icon: string) => void }) {
  const labelId = useId()

  return (
    <div className="flex flex-col gap-2">
      <span id={labelId} className="font-medium">
        ไอคอน (ไม่บังคับ)
      </span>
      <p className="text-slate-600">เลือกรูปแทนของชิ้นนี้ ไม่ต้องถ่ายภาพ</p>

      <div aria-labelledby={labelId} className="grid grid-cols-4 gap-2">
        {ICONS.map(({ icon, label }) => {
          const chosen = value === icon
          return (
            <button
              key={icon}
              type="button"
              aria-pressed={chosen}
              aria-label={label}
              // Tapping the chosen one again clears it, so undoing a mis-tap does not
              // mean hunting for a separate control.
              onClick={() => onChange(chosen ? '' : icon)}
              className={`flex min-h-16 items-center justify-center rounded-xl border-2 text-3xl ${
                chosen ? 'border-brand-700 bg-brand-100' : 'border-brand-200 bg-white'
              }`}
            >
              <span aria-hidden="true">{icon}</span>
            </button>
          )
        })}
      </div>

      {value && (
        <button
          type="button"
          onClick={() => onChange('')}
          className="-mx-2 min-h-12 self-start px-2 text-accent-700 underline underline-offset-4"
        >
          ไม่ใช้ไอคอน
        </button>
      )}
    </div>
  )
}

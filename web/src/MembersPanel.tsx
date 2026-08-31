import { useEffect, useState } from 'react'

import { createInvite, getMembers, removeMember, type Household, type Member } from './api'

/**
 * Who is in the household, and — for the owner — how to change that.
 *
 * The owner never sees or types a token: they get a link and send it in the LINE chat
 * they already use. Nothing is read aloud over the phone.
 */
export default function MembersPanel({ household, meId }: { household: Household; meId: string }) {
  const [members, setMembers] = useState<Member[] | null>(null)
  const [inviteUrl, setInviteUrl] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getMembers(household.id)
      .then(({ members }) => {
        if (!cancelled) setMembers(members)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        console.error('load members failed', err)
        setError('โหลดรายชื่อสมาชิกไม่สำเร็จ')
      })
    return () => {
      cancelled = true
    }
  }, [household.id])

  async function invite() {
    setBusy(true)
    setError(null)
    try {
      const { url } = await createInvite(household.id)
      setInviteUrl(url)
      setCopied(false)
    } catch (err) {
      console.error('create invite failed', err)
      setError('สร้างคำเชิญไม่สำเร็จ')
    } finally {
      setBusy(false)
    }
  }

  async function remove(member: Member) {
    setBusy(true)
    setError(null)
    try {
      await removeMember(household.id, member.userId)
      setMembers((current) => (current ?? []).filter((m) => m.userId !== member.userId))
    } catch (err) {
      console.error('remove member failed', err)
      setError('นำสมาชิกออกไม่สำเร็จ')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4 rounded-xl bg-slate-50 p-5">
      <h3 className="text-xl font-semibold">สมาชิกในบ้าน</h3>

      {error && (
        <p aria-live="assertive" className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900">
          <span aria-hidden="true" className="text-2xl leading-none">
            ✕
          </span>
          <span>{error}</span>
        </p>
      )}

      {members === null && <p className="text-slate-600">กำลังโหลด…</p>}

      {members && (
        <ul className="flex flex-col gap-3">
          {members.map((m) => (
            <li key={m.userId} className="flex flex-col gap-2">
              <div className="flex items-center gap-3">
                {m.pictureUrl ? (
                  <img src={m.pictureUrl} alt="" className="size-12 shrink-0 rounded-full object-cover" referrerPolicy="no-referrer" />
                ) : (
                  <span aria-hidden="true" className="flex size-12 shrink-0 items-center justify-center rounded-full bg-slate-200 text-xl">
                    👤
                  </span>
                )}
                <span className="min-w-0 flex-1 break-words">
                  {m.displayName}
                  {m.isOwner && <span className="text-slate-500"> · เจ้าของบ้าน</span>}
                </span>
              </div>
              {/* Only the owner manages people, and nobody removes themselves. */}
              {household.isOwner && !m.isOwner && m.userId !== meId && (
                <RemoveButton name={m.displayName} disabled={busy} onConfirm={() => remove(m)} />
              )}
            </li>
          ))}
        </ul>
      )}

      {household.isOwner && (
        <div className="flex flex-col gap-3">
          <button
            type="button"
            disabled={busy}
            onClick={invite}
            className="min-h-14 rounded-xl bg-slate-900 px-4 font-medium text-white disabled:bg-slate-300 disabled:text-slate-500"
          >
            {inviteUrl ? 'สร้างลิงก์ใหม่' : 'เชิญสมาชิก'}
          </button>

          {inviteUrl && (
            <div className="flex flex-col gap-2">
              <p className="text-slate-600">ส่งลิงก์นี้ให้คนที่ต้องการเชิญทาง LINE (ใช้ได้ 7 วัน)</p>
              {/* Readable and selectable, so it works even if the copy button does not. */}
              <p className="rounded-xl border-2 border-slate-300 bg-white p-3 break-all select-all">{inviteUrl}</p>
              <button
                type="button"
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(inviteUrl)
                    setCopied(true)
                  } catch (err) {
                    console.error('clipboard failed', err)
                    setError('คัดลอกไม่สำเร็จ กรุณากดค้างที่ลิงก์เพื่อคัดลอก')
                  }
                }}
                className="min-h-14 rounded-xl border-2 border-slate-300 px-4"
              >
                {copied ? 'คัดลอกแล้ว ✓' : 'คัดลอกลิงก์'}
              </button>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

/** Two steps: removing someone is the one destructive action here. */
function RemoveButton({ name, disabled, onConfirm }: { name: string; disabled: boolean; onConfirm: () => void }) {
  const [confirming, setConfirming] = useState(false)

  if (!confirming) {
    return (
      <button type="button" onClick={() => setConfirming(true)} className="min-h-12 self-start text-red-700 underline underline-offset-4">
        นำออกจากบ้าน
      </button>
    )
  }
  return (
    <div className="flex flex-col gap-2 rounded-xl bg-amber-50 p-3">
      <p className="text-amber-900">นำ “{name}” ออกจากบ้าน? ประวัติที่เคยทำไว้จะยังอยู่</p>
      <button
        type="button"
        disabled={disabled}
        onClick={onConfirm}
        className="min-h-14 rounded-xl bg-red-700 px-4 font-medium text-white disabled:bg-slate-300"
      >
        ยืนยันนำออก
      </button>
      <button type="button" onClick={() => setConfirming(false)} className="min-h-14 rounded-xl border-2 border-slate-300 px-4">
        ไม่นำออก
      </button>
    </div>
  )
}

import { useEffect, useState } from 'react'

import { acceptInvite, ApiError, getHouseholds, selectHousehold, type Household, type User } from './api'
import CreateHousehold from './CreateHousehold'
import HouseholdScreen from './HouseholdScreen'
import { resolveUser } from './signIn'

type State =
  | { status: 'loading' }
  | { status: 'needsHousehold'; user: User }
  | { status: 'ready'; user: User; households: Household[]; currentId: string }
  | { status: 'failed'; message: string }

/** Reads the invite token LIFF forwards from the link the owner sent. */
function takeInviteToken(): string | null {
  const token = new URLSearchParams(window.location.search).get('invite')
  if (!token) return null
  // Remove it so a refresh does not try to accept the same invite again.
  const url = new URL(window.location.href)
  url.searchParams.delete('invite')
  window.history.replaceState({}, '', url.toString())
  return token
}

function inviteProblem(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 410) return 'ลิงก์คำเชิญหมดอายุแล้ว ขอลิงก์ใหม่จากเจ้าของบ้าน'
    if (err.status === 409) return 'ลิงก์คำเชิญนี้ถูกใช้ไปแล้ว ขอลิงก์ใหม่จากเจ้าของบ้าน'
    if (err.status === 404) return 'ไม่พบลิงก์คำเชิญนี้'
  }
  return 'เข้าร่วมบ้านไม่สำเร็จ'
}

export default function App() {
  const [state, setState] = useState<State>({ status: 'loading' })
  const [notice, setNotice] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const inviteToken = takeInviteToken()

    async function start() {
      const user = await resolveUser()

      // Accepting the invite first, so the household it grants is present in the list
      // below and becomes the one we land in.
      if (inviteToken) {
        try {
          await acceptInvite(inviteToken)
        } catch (err) {
          console.error('accept invite failed', err)
          if (!cancelled) setNotice(inviteProblem(err))
        }
      }

      const { households, currentId } = await getHouseholds()
      if (cancelled) return

      setState(
        households.length === 0
          ? { status: 'needsHousehold', user }
          : { status: 'ready', user, households, currentId },
      )
    }

    start().catch((err: unknown) => {
      if (cancelled) return
      console.error('startup failed', err)
      setState({ status: 'failed', message: 'เข้าสู่ระบบไม่สำเร็จ กรุณาลองใหม่อีกครั้ง' })
    })

    return () => {
      cancelled = true
    }
  }, [])

  async function switchTo(householdId: string) {
    if (state.status !== 'ready') return
    setState({ ...state, currentId: householdId })
    try {
      await selectHousehold(householdId)
    } catch (err) {
      console.error('switch household failed', err)
      setNotice('สลับบ้านไม่สำเร็จ')
    }
  }

  const current = state.status === 'ready' ? state.households.find((h) => h.id === state.currentId) : undefined

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col gap-6 px-6 py-10">
      <h1 className="text-3xl font-semibold">ของใช้ในบ้าน</h1>

      {notice && (
        <p aria-live="assertive" className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900">
          <span aria-hidden="true" className="text-2xl leading-none">
            ✕
          </span>
          <span>{notice}</span>
        </p>
      )}

      {state.status === 'loading' && (
        <p aria-live="polite" className="flex min-h-12 items-center rounded-xl bg-slate-100 px-4 py-3 text-slate-700">
          กำลังเข้าสู่ระบบ…
        </p>
      )}

      {state.status === 'needsHousehold' && (
        <CreateHousehold
          onCreated={(household) =>
            setState({ status: 'ready', user: state.user, households: [household], currentId: household.id })
          }
        />
      )}

      {/* The switcher only exists for someone who belongs to more than one household.
          Everyone else must never see that this feature is there. */}
      {state.status === 'ready' && state.households.length > 1 && (
        <div className="flex flex-col gap-2">
          <label htmlFor="household-switch" className="font-medium">
            บ้าน
          </label>
          <select
            id="household-switch"
            value={state.currentId}
            onChange={(e) => void switchTo(e.target.value)}
            className="min-h-14 rounded-xl border-2 border-slate-300 bg-white px-4"
          >
            {state.households.map((h) => (
              <option key={h.id} value={h.id}>
                {h.name}
              </option>
            ))}
          </select>
        </div>
      )}

      {state.status === 'ready' && current && (
        // Keyed by household so switching resets the item list rather than showing the
        // previous household's stock while the new one loads.
        <HouseholdScreen key={current.id} household={current} user={state.user} />
      )}

      {state.status === 'failed' && (
        <section aria-live="assertive" className="flex flex-col gap-4">
          <p className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900">
            <span aria-hidden="true" className="text-2xl leading-none">
              ✕
            </span>
            <span>{state.message}</span>
          </p>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="min-h-14 rounded-xl bg-slate-900 px-4 py-3 font-medium text-white"
          >
            ลองใหม่
          </button>
        </section>
      )}
    </main>
  )
}

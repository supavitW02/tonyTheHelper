import { useEffect, useState } from 'react'

import { acceptInvite, ApiError, getHouseholds, selectHousehold, type Household, type User } from './api'
import CreateHousehold, { CreateHouseholdDialog } from './CreateHousehold'
import HouseholdScreen from './HouseholdScreen'
import { resolveUser } from './signIn'
import { inputClass, Notice, primaryButtonClass, secondaryButtonClass } from './ui'

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
  // Creating an additional household is rare, so it stays behind a deliberate tap
  // rather than occupying space next to the daily actions.
  const [creatingAnother, setCreatingAnother] = useState(false)

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

  /** A rename has to reach the switcher too, not just the header it was typed in. */
  function applyRename(renamed: Household) {
    setState((current) =>
      current.status === 'ready'
        ? { ...current, households: current.households.map((h) => (h.id === renamed.id ? renamed : h)) }
        : current,
    )
  }

  /**
   * A deleted household has to disappear from the switcher and from under the user.
   *
   * Where they land next is the interesting part: their other households if they have
   * any, and otherwise the create screen — the same place a brand new user starts, since
   * belonging to nothing is exactly the state they are now in.
   */
  function applyDeletion(householdId: string) {
    setState((current) => {
      if (current.status !== 'ready') return current
      const left = current.households.filter((h) => h.id !== householdId)
      if (left.length === 0) return { status: 'needsHousehold', user: current.user }
      return { ...current, households: left, currentId: left[0].id }
    })
  }

  const current = state.status === 'ready' ? state.households.find((h) => h.id === state.currentId) : undefined

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col gap-6 px-6 py-10">
      <h1 className="flex items-center gap-3 text-3xl font-semibold text-brand-800">
        <img src="/icon-192.png" alt="" width={192} height={192} className="size-10 shrink-0" />
        ของใช้ในบ้าน
      </h1>

      {notice && <Notice>{notice}</Notice>}

      {state.status === 'loading' && <Notice tone="info">กำลังเข้าสู่ระบบ…</Notice>}

      {state.status === 'needsHousehold' && (
        <CreateHousehold
          onCreated={(household) =>
            setState({ status: 'ready', user: state.user, households: [household], currentId: household.id })
          }
        />
      )}

      {state.status === 'ready' && (
        <div className="flex items-end gap-3">
          {/* The switcher only exists for someone who belongs to more than one household:
              with a single one there is nothing to switch to, and an empty picker would
              only be a question the user has to work out the answer to. */}
          {state.households.length > 1 && (
            <div className="flex min-w-0 flex-1 flex-col gap-2">
              <label htmlFor="household-switch" className="font-medium">
                บ้าน
              </label>
              <select
                id="household-switch"
                value={state.currentId}
                onChange={(e) => void switchTo(e.target.value)}
                className={inputClass}
              >
                {state.households.map((h) => (
                  <option key={h.id} value={h.id}>
                    {h.name}
                  </option>
                ))}
              </select>
            </div>
          )}
          <button
            type="button"
            onClick={() => setCreatingAnother(true)}
            className={`${secondaryButtonClass} shrink-0 whitespace-nowrap`}
          >
            + บ้านใหม่
          </button>
        </div>
      )}

      {state.status === 'ready' && current && (
        // Keyed by household so switching resets the item list rather than showing the
        // previous household's stock while the new one loads.
        <HouseholdScreen
          key={current.id}
          household={current}
          user={state.user}
          onRenamed={applyRename}
          onDeleted={applyDeletion}
        />
      )}

      {state.status === 'ready' && creatingAnother && (
        <CreateHouseholdDialog
          onCancel={() => setCreatingAnother(false)}
          onCreated={(household) => {
            setCreatingAnother(false)
            setState({
              ...state,
              households: [...state.households, household],
              currentId: household.id,
            })
          }}
        />
      )}

      {state.status === 'failed' && (
        <section className="flex flex-col gap-4">
          <Notice>{state.message}</Notice>
          <button type="button" onClick={() => window.location.reload()} className={primaryButtonClass}>
            ลองใหม่
          </button>
        </section>
      )}
    </main>
  )
}

import { useEffect, useState } from 'react'

import { getHouseholds, type Household, type User } from './api'
import CreateHousehold from './CreateHousehold'
import HouseholdScreen from './HouseholdScreen'
import { resolveUser } from './signIn'

type State =
  | { status: 'loading' }
  | { status: 'needsHousehold'; user: User }
  | { status: 'inHousehold'; user: User; household: Household }
  | { status: 'failed'; message: string }

export default function App() {
  const [state, setState] = useState<State>({ status: 'loading' })

  useEffect(() => {
    let cancelled = false

    async function start() {
      const user = await resolveUser()
      const { households, currentId } = await getHouseholds()
      if (cancelled) return

      const current = households.find((h) => h.id === currentId)
      setState(current ? { status: 'inHousehold', user, household: current } : { status: 'needsHousehold', user })
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

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col gap-8 px-6 py-10">
      <h1 className="text-3xl font-semibold">ของใช้ในบ้าน</h1>

      {state.status === 'loading' && (
        <p aria-live="polite" className="flex min-h-12 items-center rounded-xl bg-slate-100 px-4 py-3 text-slate-700">
          กำลังเข้าสู่ระบบ…
        </p>
      )}

      {state.status === 'needsHousehold' && (
        <CreateHousehold
          onCreated={(household) => setState({ status: 'inHousehold', user: state.user, household })}
        />
      )}

      {state.status === 'inHousehold' && <HouseholdScreen household={state.household} user={state.user} />}

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

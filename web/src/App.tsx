import { useEffect, useState } from 'react'

import type { User } from './api'
import { resolveUser } from './signIn'

type State = { status: 'loading' } | { status: 'signedIn'; user: User } | { status: 'failed'; message: string }

export default function App() {
  const [state, setState] = useState<State>({ status: 'loading' })

  useEffect(() => {
    let cancelled = false

    resolveUser()
      .then((user) => {
        if (!cancelled) setState({ status: 'signedIn', user })
      })
      .catch((err: unknown) => {
        if (cancelled) return
        console.error('sign-in failed', err)
        setState({ status: 'failed', message: 'เข้าสู่ระบบไม่สำเร็จ กรุณาลองใหม่อีกครั้ง' })
      })

    return () => {
      cancelled = true
    }
  }, [])

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-8 px-6 py-12">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold">ของใช้ในบ้าน</h1>
        <p className="text-slate-600">ระบบจัดการของใช้ในบ้านสำหรับครอบครัว</p>
      </header>

      {state.status === 'loading' && <Message>กำลังเข้าสู่ระบบ…</Message>}
      {state.status === 'signedIn' && <Greeting user={state.user} />}
      {state.status === 'failed' && <Failure message={state.message} />}
    </main>
  )
}

function Message({ children }: { children: React.ReactNode }) {
  return (
    <p aria-live="polite" className="flex min-h-12 items-center rounded-xl bg-slate-100 px-4 py-3 text-slate-700">
      {children}
    </p>
  )
}

function Greeting({ user }: { user: User }) {
  return (
    <section aria-live="polite" className="flex items-center gap-4 rounded-xl bg-emerald-50 px-4 py-4">
      {user.pictureUrl ? (
        <img
          src={user.pictureUrl}
          alt=""
          className="size-16 shrink-0 rounded-full object-cover"
          referrerPolicy="no-referrer"
        />
      ) : (
        <span aria-hidden="true" className="flex size-16 shrink-0 items-center justify-center rounded-full bg-emerald-100 text-2xl">
          👤
        </span>
      )}
      <span className="text-emerald-900">
        สวัสดี <strong className="font-semibold">{user.displayName}</strong>
      </span>
    </section>
  )
}

function Failure({ message }: { message: string }) {
  return (
    <section aria-live="assertive" className="flex flex-col gap-4">
      <p className="flex min-h-12 items-center gap-3 rounded-xl bg-amber-50 px-4 py-3 text-amber-900">
        <span aria-hidden="true" className="text-2xl leading-none">
          ✕
        </span>
        <span>{message}</span>
      </p>
      {/* A full reload restarts the whole sign-in flow, including LIFF's own state. */}
      <button
        type="button"
        onClick={() => window.location.reload()}
        className="min-h-12 rounded-xl bg-slate-900 px-4 py-3 font-medium text-white"
      >
        ลองใหม่
      </button>
    </section>
  )
}

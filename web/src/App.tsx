import { useEffect, useState } from 'react'

type Health = 'checking' | 'ok' | 'unreachable'

/**
 * The walking skeleton (issue #1): a Thai page, served by the Go binary it is
 * embedded in, that proves the whole path works by calling the API's own health
 * check. Every later ticket replaces this content.
 */
export default function App() {
  const health = useHealth()

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-8 px-6 py-12">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold">ของใช้ในบ้าน</h1>
        <p className="text-slate-600">ระบบจัดการของใช้ในบ้านสำหรับครอบครัว</p>
      </header>
      <ServerStatus health={health} />
    </main>
  )
}

function useHealth(): Health {
  const [health, setHealth] = useState<Health>('checking')

  useEffect(() => {
    const aborted = new AbortController()

    fetch('/healthz', { signal: aborted.signal })
      .then((res) => {
        if (!res.ok) throw new Error(`healthz returned ${res.status}`)
        setHealth('ok')
      })
      .catch((err: unknown) => {
        if (err instanceof Error && err.name === 'AbortError') return
        setHealth('unreachable')
      })

    return () => aborted.abort()
  }, [])

  return health
}

// Status is carried by both a symbol and words, never by colour alone — the app's
// primary user should not have to distinguish red from green to understand it.
const STATUS: Record<Health, { mark: string; label: string; className: string }> = {
  checking: {
    mark: '…',
    label: 'กำลังเชื่อมต่อเซิร์ฟเวอร์',
    className: 'bg-slate-100 text-slate-700',
  },
  ok: {
    mark: '✓',
    label: 'เชื่อมต่อเซิร์ฟเวอร์แล้ว',
    className: 'bg-emerald-50 text-emerald-900',
  },
  unreachable: {
    mark: '✕',
    label: 'เชื่อมต่อเซิร์ฟเวอร์ไม่สำเร็จ',
    className: 'bg-amber-50 text-amber-900',
  },
}

function ServerStatus({ health }: { health: Health }) {
  const { mark, label, className } = STATUS[health]

  return (
    <p
      // aria-live so the status is announced when it resolves, not silently swapped.
      aria-live="polite"
      className={`flex min-h-12 items-center gap-3 rounded-xl px-4 py-3 ${className}`}
    >
      <span aria-hidden="true" className="text-2xl leading-none">
        {mark}
      </span>
      <span>{label}</span>
    </p>
  )
}

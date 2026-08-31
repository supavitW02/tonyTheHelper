/** The signed-in user, as the API describes them. */
export type User = {
  id: string
  displayName: string
  pictureUrl?: string
}

/** An API call that failed in a way the UI needs to distinguish. */
export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })

  if (!res.ok) {
    // The API sends {"error": "..."} but a proxy or a crash might not.
    const detail = await res
      .json()
      .then((b: { error?: string }) => b.error)
      .catch(() => undefined)
    throw new ApiError(res.status, detail ?? `request to ${path} failed`)
  }

  return res.json() as Promise<T>
}

/** The LIFF id, served at runtime so changing it needs no rebuild. */
export const getConfig = () => request<{ liffId: string }>('/api/config')

/** The current user, or an ApiError with status 401 when not signed in. */
export const getMe = () => request<User>('/api/me')

/** Exchanges a LINE ID token for a session cookie. */
export const signIn = (idToken: string) =>
  request<User>('/api/session', {
    method: 'POST',
    body: JSON.stringify({ idToken }),
  })

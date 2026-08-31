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

/** A group of people who share one stock of items. */
export type Household = {
  id: string
  name: string
  /** Only the owner manages who is in the household. */
  isOwner: boolean
}

/**
 * The households the signed-in user belongs to, and which one to open.
 * An empty `currentId` means they belong to none yet.
 */
export const getHouseholds = () => request<{ households: Household[]; currentId: string }>('/api/households')

/** Creates a household with the caller as its owner and first member. */
export const createHousehold = (name: string) =>
  request<Household>('/api/households', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })

/** A kind of thing a household keeps in stock. */
export type Item = {
  id: string
  name: string
  unit: string
  description?: string
  quantity: number
}

/** A household's items, plus the units it already uses (offered as chips). */
export const getItems = (householdId: string) =>
  request<{ items: Item[]; units: string[] }>(`/api/households/${householdId}/items`)

export const createItem = (householdId: string, item: { name: string; unit: string; description?: string }) =>
  request<Item>(`/api/households/${householdId}/items`, {
    method: 'POST',
    body: JSON.stringify(item),
  })

/**
 * Changes an item's quantity. Send `delta` for the plus/minus buttons or `quantity` to
 * set a total — never both. The server does the arithmetic so the movement log can never
 * disagree with the stored quantity.
 */
export const adjustQuantity = (itemId: string, change: { delta?: number; quantity?: number; note?: string }) =>
  request<Item>(`/api/items/${itemId}/quantity`, {
    method: 'POST',
    body: JSON.stringify(change),
  })

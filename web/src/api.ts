import { resizeImage } from './resizeImage'

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

/** Changes a household's name. Owner only; a member gets 403 and a stranger 404. */
export const renameHousehold = (householdId: string, name: string) =>
  request<Household>(`/api/households/${householdId}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  })

/**
 * Makes a household inaccessible to everyone in it. Owner only.
 *
 * The items and their history are not erased, only unreachable — the movement log is
 * append-only (ADR-0001). From the app's side there is no way back.
 */
export const deleteHousehold = async (householdId: string): Promise<void> => {
  const res = await fetch(`/api/households/${householdId}`, { method: 'DELETE' })
  if (!res.ok) throw new ApiError(res.status, 'could not delete the household')
}

/** A kind of thing a household keeps in stock. */
export type Item = {
  id: string
  name: string
  unit: string
  description?: string
  quantity: number
  /** Absent means the item is never low. */
  lowStockThreshold?: number
  /** A member said by hand that this is running out, whatever the threshold says. */
  markedLow: boolean
  /** Threshold or mark, whichever fires. Derived by the server, so the UI never
   *  disagrees with the list ordering. */
  isLow: boolean
  /** Whether a photo exists. The storage path is deliberately not exposed. */
  hasImage: boolean
  /** A symbol chosen instead of a photo. A photo, if there is one, wins. */
  icon?: string
}

/** The writable half of an item, shared by add and edit. */
export type ItemInput = {
  name: string
  unit: string
  description?: string
  lowStockThreshold?: number | null
  /** Empty clears any icon the item had. */
  icon?: string
}

/** One recorded change to an item's quantity, with the person who made it. */
export type Movement = {
  id: string
  actorName: string
  actorPictureUrl?: string
  quantityBefore: number
  quantityAfter: number
  note?: string
  createdAt: string
}

/** A household's items, plus the units it already uses (offered as chips). */
export const getItems = (householdId: string) =>
  request<{ items: Item[]; units: string[] }>(`/api/households/${householdId}/items`)

export const createItem = (householdId: string, item: ItemInput) =>
  request<Item>(`/api/households/${householdId}/items`, {
    method: 'POST',
    body: JSON.stringify(item),
  })

export const updateItem = (itemId: string, item: ItemInput) =>
  request<Item>(`/api/items/${itemId}`, {
    method: 'PUT',
    body: JSON.stringify(item),
  })

/** Soft-deletes an item. Its history stays readable afterwards. */
export const deleteItem = async (itemId: string): Promise<void> => {
  const res = await fetch(`/api/items/${itemId}`, { method: 'DELETE' })
  if (!res.ok) throw new ApiError(res.status, `could not delete item ${itemId}`)
}

/** An item's history, newest first. */
export const getMovements = (itemId: string) =>
  request<{ movements: Movement[] }>(`/api/items/${itemId}/movements`)

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

/**
 * Puts an item on the shopping list by hand, or takes it back off.
 *
 * For the things a household keeps exactly one of — a bottle of shampoo sits at 1 from
 * the day it is bought until the day it is empty, so no threshold can tell the two apart.
 * PUT, not POST: the body says what the flag should be, so a retry is harmless and two
 * people noticing the same empty bottle cannot cancel each other out.
 */
export const setItemMarkedLow = (itemId: string, markedLow: boolean) =>
  request<Item>(`/api/items/${itemId}/low`, {
    method: 'PUT',
    body: JSON.stringify({ markedLow }),
  })

/** The URL an item's photo is served from. Redirects to a freshly signed storage URL. */
export const itemImageUrl = (itemId: string) => `/api/items/${itemId}/image`

/**
 * Uploads a photo for an item.
 *
 * Three steps, deliberately: the bytes go straight from the phone to object storage, so a
 * 256MB machine in Singapore never proxies a photo, and the item only claims to have an
 * image once the transfer has actually succeeded.
 */
export async function uploadItemPhoto(itemId: string, file: File): Promise<Item> {
  const blob = await resizeImage(file)

  const { uploadUrl, path } = await request<{ uploadUrl: string; path: string }>(
    `/api/items/${itemId}/image`,
    { method: 'POST' },
  )

  // No custom headers: anything beyond a simple Content-Type would turn this into a
  // CORS preflight against storage.
  const upload = await fetch(uploadUrl, {
    method: 'PUT',
    body: blob,
    headers: { 'Content-Type': 'image/jpeg' },
  })
  if (!upload.ok) throw new ApiError(upload.status, 'photo upload failed')

  return request<Item>(`/api/items/${itemId}/image`, {
    method: 'PUT',
    body: JSON.stringify({ path }),
  })
}

/**
 * A change to any item in the household, for the history tab.
 *
 * It names its own item, which a single item's history does not need, and carries no
 * picture: the tab is a list of changes to read down, not people to look at.
 */
export type HouseholdMovement = {
  id: string
  itemId: string
  itemName: string
  unit: string
  actorName: string
  quantityBefore: number
  quantityAfter: number
  note?: string
  createdAt: string
}

/** Just enough of an item to name it in the filter. */
export type ItemRef = {
  id: string
  name: string
}

/**
 * Recent changes across the whole household, newest first, plus the items the filter can
 * narrow to. Both come back together so opening the tab is one request.
 */
export const getHouseholdHistory = (householdId: string, filter: { itemId?: string; q?: string } = {}) => {
  const params = new URLSearchParams()
  if (filter.itemId) params.set('itemId', filter.itemId)
  if (filter.q) params.set('q', filter.q)
  const query = params.toString()
  return request<{ movements: HouseholdMovement[]; items: ItemRef[] }>(
    `/api/households/${householdId}/movements${query ? `?${query}` : ''}`,
  )
}

/** A person in a household, as the members screen shows them. */
export type Member = {
  userId: string
  displayName: string
  pictureUrl?: string
  isOwner: boolean
}

export const getMembers = (householdId: string) =>
  request<{ members: Member[] }>(`/api/households/${householdId}/members`)

/** Mints an invite and returns the LINE link to send. Owner only. */
export const createInvite = (householdId: string) =>
  request<{ url: string; expiresAt: string }>(`/api/households/${householdId}/invites`, { method: 'POST' })

/** Revokes access. The person and their history are untouched. Owner only. */
export const removeMember = async (householdId: string, userId: string): Promise<void> => {
  const res = await fetch(`/api/households/${householdId}/members/${userId}`, { method: 'DELETE' })
  if (!res.ok) throw new ApiError(res.status, 'could not remove the member')
}

/** Joins the household an invite belongs to. */
export const acceptInvite = (token: string) =>
  request<Household>(`/api/invites/${encodeURIComponent(token)}/accept`, { method: 'POST' })

/** Records which household is being looked at, so the next open lands there. */
export const selectHousehold = async (householdId: string): Promise<void> => {
  const res = await fetch(`/api/households/${householdId}/select`, { method: 'POST' })
  if (!res.ok) throw new ApiError(res.status, 'could not switch household')
}

import liff from '@line/liff'

import { ApiError, getConfig, getMe, signIn, type User } from './api'

/**
 * Resolves the current user, signing in through LINE if needed.
 *
 * The session cookie is checked first: someone who already has one never touches
 * LIFF at all, which keeps a repeat open fast and works even if LINE is slow.
 */
export async function resolveUser(): Promise<User> {
  try {
    return await getMe()
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 401) throw err
  }

  const { liffId } = await getConfig()
  await liff.init({ liffId })

  if (!liff.isLoggedIn()) {
    // Redirects away and comes back; nothing after this line runs.
    liff.login()
    return new Promise<User>(() => {})
  }

  const idToken = liff.getIDToken()
  if (!idToken) throw new Error('LINE did not return an ID token')

  return signIn(idToken)
}

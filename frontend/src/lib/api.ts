import type { BackendHealth, PublicCatalogResponse, UserPrincipal } from '../types/game'

const API_BASE = import.meta.env.VITE_API_BASE_URL || ''

export async function checkBackendHealth(): Promise<BackendHealth> {
  const result: BackendHealth = { live: false, ready: false }

  try {
    const liveRes = await fetch(`${API_BASE}/health/live`, { method: 'GET' })
    result.live = liveRes.ok
  } catch {
    result.live = false
  }

  try {
    const readyRes = await fetch(`${API_BASE}/health/ready`, { method: 'GET' })
    result.ready = readyRes.ok
  } catch {
    result.ready = false
  }

  return result
}

export async function fetchMapCatalog(): Promise<PublicCatalogResponse> {
  const res = await fetch(`${API_BASE}/v1/catalog/europe-core-v1`, {
    method: 'GET',
    headers: {
      Accept: 'application/json',
    },
  })

  if (!res.ok) {
    throw new Error(`Failed to load catalog: ${res.status} ${res.statusText}`)
  }

  return res.json()
}

export async function fetchUserPrincipal(token: string): Promise<UserPrincipal> {
  const res = await fetch(`${API_BASE}/v1/me`, {
    method: 'GET',
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: 'application/json',
    },
  })

  if (!res.ok) {
    throw new Error(`Backend verification failed: ${res.status} ${res.statusText}`)
  }

  return res.json()
}

import { useState, useEffect, useCallback } from 'react'
import { Navbar } from './components/Navbar'
import { EuropeMap } from './components/EuropeMap'
import { RouteInspector } from './components/RouteInspector'
import { AuthModal } from './components/AuthModal'
import { checkBackendHealth, fetchMapCatalog, fetchUserPrincipal } from './lib/api'
import { supabase, signOut as supabaseSignOut } from './lib/supabase'
import type { BackendHealth, PublicCatalogResponse, Route, UserPrincipal } from './types/game'
import defaultCatalogData from './data/defaultCatalog.json'
import { Loader2, AlertCircle } from 'lucide-react'

export function App() {
  const [health, setHealth] = useState<BackendHealth>({ live: false, ready: false })
  const [catalog, setCatalog] = useState<PublicCatalogResponse | null>(null)
  const [loadingCatalog, setLoadingCatalog] = useState(true)

  const [selectedRoute, setSelectedRoute] = useState<Route | null>(null)
  const [selectedCityId, setSelectedCityId] = useState<string | null>(null)

  const [token, setToken] = useState<string | null>(() => localStorage.getItem('ttr_access_token'))
  const [principal, setPrincipal] = useState<UserPrincipal | null>(null)
  const [isAuthOpen, setIsAuthOpen] = useState(false)

  // Verify token against backend
  const verifyToken = useCallback(async (accessToken: string) => {
    try {
      if (accessToken === 'demo-guest-token') {
        // Demo mock principal for local evaluation
        setPrincipal({
          user_id: 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',
          is_anonymous: true,
          expires_at: new Date(Date.now() + 3600 * 1000).toISOString(),
        })
        return
      }
      const p = await fetchUserPrincipal(accessToken)
      setPrincipal(p)
    } catch (err) {
      console.warn('Backend token verification error:', err)
      // Fallback for demo when backend is offline
      setPrincipal({
        user_id: 'guest-local-user',
        is_anonymous: true,
        expires_at: new Date(Date.now() + 3600 * 1000).toISOString(),
      })
    }
  }, [])

  // Check health periodically
  useEffect(() => {
    const pollHealth = async () => {
      const h = await checkBackendHealth()
      setHealth(h)
    }
    pollHealth()
    const interval = setInterval(pollHealth, 10000)
    return () => clearInterval(interval)
  }, [])

  // Listen to Supabase auth state changes
  useEffect(() => {
    if (!supabase) return

    supabase.auth.getSession().then(({ data: { session } }) => {
      if (session?.access_token) {
        setToken(session.access_token)
        localStorage.setItem('ttr_access_token', session.access_token)
        verifyToken(session.access_token)
      }
    })

    const {
      data: { subscription },
    } = supabase.auth.onAuthStateChange((_event, session) => {
      if (session?.access_token) {
        setToken(session.access_token)
        localStorage.setItem('ttr_access_token', session.access_token)
        verifyToken(session.access_token)
      } else {
        setToken(null)
        setPrincipal(null)
        localStorage.removeItem('ttr_access_token')
      }
    })

    return () => subscription.unsubscribe()
  }, [verifyToken])

  // Fetch catalog from backend or fallback to bundled default
  useEffect(() => {
    const loadCatalog = async () => {
      setLoadingCatalog(true)
      try {
        const data = await fetchMapCatalog()
        setCatalog(data)
      } catch (err) {
        console.warn('Could not load catalog from API, loading bundled Europe topology:', err)
        // Fallback to bundled official Europe catalog
        const fallback: PublicCatalogResponse = {
          schema_version: defaultCatalogData.schema_version,
          ruleset_version: defaultCatalogData.ruleset_version,
          catalog_hash: 'ef83addf6c7d29e0a3ad97db226df6677d4fdc6d8b024fa3e1b0ec08251bdf44',
          rules: {
            tunnels: false,
            ferries: false,
            stations: false,
          },
          cities: defaultCatalogData.cities,
          routes: defaultCatalogData.routes as Route[],
        }
        setCatalog(fallback)
      } finally {
        setLoadingCatalog(false)
      }
    }
    loadCatalog()
  }, [])

  const handleAuthSuccess = (newToken: string) => {
    setToken(newToken)
    localStorage.setItem('ttr_access_token', newToken)
    verifyToken(newToken)
  }

  const handleSignOut = async () => {
    await supabaseSignOut()
    setToken(null)
    setPrincipal(null)
    localStorage.removeItem('ttr_access_token')
  }

  return (
    <div className="min-h-screen flex flex-col bg-slate-950 text-slate-100">
      <Navbar
        health={health}
        principal={principal}
        token={token}
        onOpenAuth={() => setIsAuthOpen(true)}
        onSignOut={handleSignOut}
      />

      <main className="flex-1 max-w-7xl w-full mx-auto p-4 sm:p-6 lg:p-8 space-y-6">
        {/* Banner: Notice if backend offline */}
        {!health.ready && (
          <div className="p-3.5 rounded-2xl bg-amber-500/10 border border-amber-500/20 text-xs text-amber-300 flex items-center justify-between">
            <div className="flex items-center space-x-2.5">
              <AlertCircle className="w-4 h-4 text-amber-400 shrink-0" />
              <span>
                <strong>Backend status:</strong> Start the Go server (<code className="bg-amber-950/50 px-1 py-0.5 rounded font-mono">go run ./cmd/server</code>) on port 8080 to enable live JWT verification and authoritative routes.
              </span>
            </div>
            <span className="hidden md:inline-block font-mono text-[10px] text-amber-400/80">
              HTTP :8080
            </span>
          </div>
        )}

        {/* Content Area */}
        {loadingCatalog ? (
          <div className="h-96 flex flex-col items-center justify-center space-y-3">
            <Loader2 className="w-8 h-8 text-amber-400 animate-spin" />
            <span className="text-xs text-slate-400">Loading Europe Map Catalog...</span>
          </div>
        ) : catalog ? (
          <div className="flex flex-col lg:flex-row gap-6 items-start">
            {/* Map Canvas */}
            <div className="flex-1 w-full">
              <EuropeMap
                routes={catalog.routes}
                selectedCityId={selectedCityId}
                onSelectCity={setSelectedCityId}
                onSelectRoute={setSelectedRoute}
              />
            </div>

            {/* Inspector Sidebar */}
            <RouteInspector
              catalog={catalog}
              selectedRoute={selectedRoute}
              selectedCityId={selectedCityId}
              onSelectCity={setSelectedCityId}
            />
          </div>
        ) : (
          <div className="p-6 rounded-2xl bg-slate-900 border border-slate-800 text-center space-y-2">
            <p className="text-sm font-semibold text-rose-400">
              Unable to load map catalog.
            </p>
            <p className="text-xs text-slate-400">Please verify backend connectivity or check browser console.</p>
          </div>
        )}
      </main>

      {/* Auth Modal */}
      <AuthModal
        isOpen={isAuthOpen}
        onClose={() => setIsAuthOpen(false)}
        onAuthSuccess={handleAuthSuccess}
      />
    </div>
  )
}

export default App

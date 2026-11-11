import React, { useState } from 'react'
import { X, UserPlus, LogIn, Sparkles, Shield, AlertCircle } from 'lucide-react'
import {
  isSupabaseConfigured,
  signInAsGuest,
  signInWithEmail,
  signUpWithEmail,
} from '../lib/supabase'

interface AuthModalProps {
  isOpen: boolean
  onClose: () => void
  onAuthSuccess: (token: string) => void
}

export const AuthModal: React.FC<AuthModalProps> = ({
  isOpen,
  onClose,
  onAuthSuccess,
}) => {
  const [tab, setTab] = useState<'guest' | 'email'>('guest')
  const [isSignUp, setIsSignUp] = useState(false)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  if (!isOpen) return null

  const handleGuestSignIn = async () => {
    setLoading(true)
    setErrorMsg(null)

    if (!isSupabaseConfigured) {
      // Demo guest mode token for testing UI when Supabase is not configured
      const demoToken = 'demo-guest-token'
      setTimeout(() => {
        setLoading(false)
        onAuthSuccess(demoToken)
        onClose()
      }, 500)
      return
    }

    const { session, error } = await signInAsGuest()
    setLoading(false)

    if (error) {
      setErrorMsg(error.message)
      return
    }

    if (session?.access_token) {
      onAuthSuccess(session.access_token)
      onClose()
    }
  }

  const handleEmailAuth = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setErrorMsg(null)

    if (!isSupabaseConfigured) {
      setErrorMsg(
        'Supabase is not configured in frontend/.env. Set VITE_SUPABASE_URL and VITE_SUPABASE_ANON_KEY to enable email authentication.'
      )
      setLoading(false)
      return
    }

    const authFn = isSignUp ? signUpWithEmail : signInWithEmail
    const { session, error } = await authFn(email, password)
    setLoading(false)

    if (error) {
      setErrorMsg(error.message)
      return
    }

    if (session?.access_token) {
      onAuthSuccess(session.access_token)
      onClose()
    } else if (isSignUp) {
      setErrorMsg('Signup successful! Please check your email to confirm or sign in.')
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm animate-fade-in">
      <div className="relative w-full max-w-md rounded-2xl bg-slate-900 border border-slate-800 p-6 shadow-2xl shadow-black/60">
        {/* Close Button */}
        <button
          onClick={onClose}
          className="absolute top-4 right-4 p-1.5 text-slate-400 hover:text-white rounded-lg hover:bg-slate-800 transition-colors"
        >
          <X className="w-5 h-5" />
        </button>

        {/* Header */}
        <div className="mb-6">
          <div className="flex items-center space-x-2 text-amber-400 font-semibold text-sm mb-1">
            <Shield className="w-4 h-4" />
            <span>Identity & Access</span>
          </div>
          <h2 className="text-xl font-bold text-white tracking-tight">
            Join Ticket to Ride
          </h2>
          <p className="text-xs text-slate-400 mt-1">
            Verified by Supabase Auth and the authoritative Go backend.
          </p>
        </div>

        {/* Config Notice if Supabase not configured */}
        {!isSupabaseConfigured && (
          <div className="mb-4 p-3 rounded-xl bg-amber-500/10 border border-amber-500/20 text-xs text-amber-300 flex items-start space-x-2">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5 text-amber-400" />
            <div>
              <span className="font-semibold">Local Dev Notice:</span> Supabase keys are not set in <code className="bg-amber-950/50 px-1 py-0.5 rounded">frontend/.env</code>. Quick Guest will start in demo preview mode.
            </div>
          </div>
        )}

        {/* Tab Switcher */}
        <div className="flex p-1 rounded-xl bg-slate-950/60 border border-slate-800/80 mb-5">
          <button
            onClick={() => {
              setTab('guest')
              setErrorMsg(null)
            }}
            className={`flex-1 py-2 rounded-lg text-xs font-semibold transition-all flex items-center justify-center space-x-1.5 ${
              tab === 'guest'
                ? 'bg-amber-500 text-slate-950 shadow-sm'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <Sparkles className="w-3.5 h-3.5" />
            <span>Anonymous Guest</span>
          </button>
          <button
            onClick={() => {
              setTab('email')
              setErrorMsg(null)
            }}
            className={`flex-1 py-2 rounded-lg text-xs font-semibold transition-all flex items-center justify-center space-x-1.5 ${
              tab === 'email'
                ? 'bg-slate-800 text-white shadow-sm'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <LogIn className="w-3.5 h-3.5" />
            <span>Email & Password</span>
          </button>
        </div>

        {errorMsg && (
          <div className="mb-4 p-3 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-300 text-xs">
            {errorMsg}
          </div>
        )}

        {tab === 'guest' ? (
          <div className="space-y-4">
            <div className="p-4 rounded-xl bg-slate-950/50 border border-slate-800 text-xs text-slate-300 space-y-2">
              <p className="font-medium text-slate-200">
                1-Click Instant Guest Sign-In:
              </p>
              <ul className="list-disc list-inside space-y-1 text-slate-400">
                <li>No email or password needed</li>
                <li>Generates a verified UUID identity with guest claim</li>
                <li>Accepted by the Go server JWT verification pipeline</li>
              </ul>
            </div>

            <button
              onClick={handleGuestSignIn}
              disabled={loading}
              className="w-full py-2.5 rounded-xl bg-gradient-to-r from-amber-500 to-amber-600 hover:from-amber-400 hover:to-amber-500 text-slate-950 font-semibold text-sm transition-all shadow-lg shadow-amber-500/20 active:scale-98 disabled:opacity-50"
            >
              {loading ? 'Authenticating...' : 'Play as Guest'}
            </button>
          </div>
        ) : (
          <form onSubmit={handleEmailAuth} className="space-y-3.5">
            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">
                Email Address
              </label>
              <input
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                className="w-full px-3 py-2 rounded-xl bg-slate-950 border border-slate-800 text-white text-xs focus:outline-none focus:border-amber-500 transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">
                Password
              </label>
              <input
                type="password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="w-full px-3 py-2 rounded-xl bg-slate-950 border border-slate-800 text-white text-xs focus:outline-none focus:border-amber-500 transition-colors"
              />
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full py-2.5 rounded-xl bg-amber-500 hover:bg-amber-400 text-slate-950 font-semibold text-sm transition-all shadow-md shadow-amber-500/20 active:scale-98 disabled:opacity-50"
            >
              {loading
                ? 'Processing...'
                : isSignUp
                ? 'Create Account'
                : 'Sign In with Email'}
            </button>

            <div className="text-center pt-2">
              <button
                type="button"
                onClick={() => setIsSignUp(!isSignUp)}
                className="text-xs text-amber-400 hover:underline inline-flex items-center space-x-1"
              >
                <UserPlus className="w-3.5 h-3.5" />
                <span>
                  {isSignUp
                    ? 'Already have an account? Sign in'
                    : "Don't have an account? Sign up"}
                </span>
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}

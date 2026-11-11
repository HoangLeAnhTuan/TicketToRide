import React from 'react'
import { Train, User, ShieldCheck, LogOut, Activity, Wifi, WifiOff } from 'lucide-react'
import type { BackendHealth, UserPrincipal } from '../types/game'

interface NavbarProps {
  health: BackendHealth
  principal: UserPrincipal | null
  token: string | null
  onOpenAuth: () => void
  onSignOut: () => void
}

export const Navbar: React.FC<NavbarProps> = ({
  health,
  principal,
  token,
  onOpenAuth,
  onSignOut,
}) => {
  return (
    <header className="sticky top-0 z-40 border-b border-slate-800 bg-slate-950/80 backdrop-blur-md">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
        {/* Brand */}
        <div className="flex items-center space-x-3">
          <div className="w-10 h-10 rounded-xl bg-gradient-to-tr from-amber-600 to-amber-400 flex items-center justify-center shadow-lg shadow-amber-500/20">
            <Train className="w-5 h-5 text-slate-950 stroke-[2.5]" />
          </div>
          <div>
            <div className="flex items-center space-x-2">
              <span className="font-bold text-lg tracking-tight text-white">Ticket to Ride</span>
              <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-400 border border-amber-500/20">
                Europe
              </span>
            </div>
            <p className="text-xs text-slate-400 font-mono">Authoritative Engine • Phase 1</p>
          </div>
        </div>

        {/* Status Indicators & Auth */}
        <div className="flex items-center space-x-3">
          {/* Backend Status */}
          <div
            className={`hidden sm:flex items-center space-x-2 px-3 py-1.5 rounded-lg border text-xs font-medium ${
              health.ready
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : health.live
                ? 'bg-amber-500/10 border-amber-500/30 text-amber-300'
                : 'bg-rose-500/10 border-rose-500/30 text-rose-300'
            }`}
          >
            {health.ready ? (
              <>
                <span className="relative flex h-2 w-2">
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                  <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
                </span>
                <Wifi className="w-3.5 h-3.5" />
                <span>Go Backend Ready</span>
              </>
            ) : health.live ? (
              <>
                <Activity className="w-3.5 h-3.5 text-amber-400" />
                <span>Backend Warming</span>
              </>
            ) : (
              <>
                <WifiOff className="w-3.5 h-3.5 text-rose-400" />
                <span>Backend Offline</span>
              </>
            )}
          </div>

          {/* User Section */}
          {token && principal ? (
            <div className="flex items-center space-x-2">
              <div className="flex items-center space-x-2 px-3 py-1.5 rounded-lg bg-slate-900 border border-slate-800 text-xs">
                <div
                  className={`w-2 h-2 rounded-full ${
                    principal.is_anonymous ? 'bg-amber-400' : 'bg-indigo-400'
                  }`}
                />
                <User className="w-3.5 h-3.5 text-slate-400" />
                <span className="font-mono text-slate-300">
                  {principal.is_anonymous ? 'Guest' : 'User'}:{' '}
                  {principal.user_id.slice(0, 8)}...
                </span>
                <span className="flex items-center text-emerald-400" title="Verified by Go Backend (/v1/me)">
                  <ShieldCheck className="w-3.5 h-3.5 ml-1" />
                </span>
              </div>
              <button
                onClick={onSignOut}
                className="p-1.5 text-slate-400 hover:text-rose-400 hover:bg-slate-900 rounded-lg transition-colors"
                title="Sign Out"
              >
                <LogOut className="w-4 h-4" />
              </button>
            </div>
          ) : (
            <button
              onClick={onOpenAuth}
              className="px-3.5 py-1.5 rounded-lg bg-gradient-to-r from-amber-500 to-amber-600 hover:from-amber-400 hover:to-amber-500 text-slate-950 font-semibold text-xs transition-all shadow-md shadow-amber-500/20 active:scale-95"
            >
              Sign In / Guest
            </button>
          )}
        </div>
      </div>
    </header>
  )
}

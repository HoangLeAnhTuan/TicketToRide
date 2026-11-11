import React from 'react'
import { MapPin, Navigation, Info, ShieldAlert, Anchor } from 'lucide-react'
import type { Route, PublicCatalogResponse } from '../types/game'
import { CITY_COORDINATES } from '../data/cityCoordinates'

interface RouteInspectorProps {
  catalog: PublicCatalogResponse | null
  selectedRoute: Route | null
  selectedCityId: string | null
  onSelectCity: (cityId: string | null) => void
}

const SCORING_TABLE: Record<number, number> = {
  1: 1,
  2: 2,
  3: 4,
  4: 7,
  6: 15,
  8: 21,
}

export const RouteInspector: React.FC<RouteInspectorProps> = ({
  catalog,
  selectedRoute,
  selectedCityId,
  onSelectCity,
}) => {
  const selectedCity = selectedCityId ? CITY_COORDINATES[selectedCityId] : null
  const connectedRoutes = selectedCityId && catalog
    ? catalog.routes.filter((r) => r.a === selectedCityId || r.b === selectedCityId)
    : []

  return (
    <aside className="w-full lg:w-80 space-y-4">
      {/* Route or City Inspector Card */}
      <div className="p-4 rounded-2xl bg-slate-900 border border-slate-800 shadow-xl backdrop-blur-md">
        <h3 className="text-xs font-bold uppercase tracking-wider text-amber-400 flex items-center space-x-1.5 mb-3">
          <Info className="w-3.5 h-3.5" />
          <span>Inspector</span>
        </h3>

        {selectedRoute ? (
          <div className="space-y-3">
            <div className="flex items-center justify-between pb-2 border-b border-slate-800">
              <span className="text-sm font-bold text-white flex items-center space-x-1.5">
                <Navigation className="w-4 h-4 text-amber-400" />
                <span>
                  {CITY_COORDINATES[selectedRoute.a]?.name} ↔{' '}
                  {CITY_COORDINATES[selectedRoute.b]?.name}
                </span>
              </span>
              <span
                className="px-2 py-0.5 rounded text-[11px] font-bold text-slate-950 uppercase"
                style={{
                  backgroundColor:
                    selectedRoute.color === 'WHITE' ? '#cbd5e1' : undefined,
                }}
              >
                {selectedRoute.color}
              </span>
            </div>

            <div className="grid grid-cols-2 gap-2 text-xs">
              <div className="p-2.5 rounded-xl bg-slate-950/60 border border-slate-800">
                <span className="text-slate-400 block text-[10px]">Track Length</span>
                <span className="text-base font-bold text-white">
                  {selectedRoute.length}{' '}
                  <span className="text-xs font-normal text-slate-400">wagons</span>
                </span>
              </div>
              <div className="p-2.5 rounded-xl bg-slate-950/60 border border-slate-800">
                <span className="text-slate-400 block text-[10px]">Points Value</span>
                <span className="text-base font-bold text-emerald-400">
                  +{SCORING_TABLE[selectedRoute.length] || 0}{' '}
                  <span className="text-xs font-normal text-slate-400">pts</span>
                </span>
              </div>
            </div>

            {selectedRoute.source_kind === 'TUNNEL' && (
              <div className="p-2.5 rounded-xl bg-amber-500/10 border border-amber-500/20 text-xs text-amber-300 flex items-center space-x-2">
                <ShieldAlert className="w-4 h-4 text-amber-400 shrink-0" />
                <span>Tunnel route (MVP treats as standard claim)</span>
              </div>
            )}

            {selectedRoute.source_locomotives > 0 && (
              <div className="p-2.5 rounded-xl bg-sky-500/10 border border-sky-500/20 text-xs text-sky-300 flex items-center space-x-2">
                <Anchor className="w-4 h-4 text-sky-400 shrink-0" />
                <span>
                  Ferry route: {selectedRoute.source_locomotives} locomotive(s)
                </span>
              </div>
            )}

            {selectedRoute.parallel_group && (
              <div className="text-[11px] text-slate-400 italic">
                Part of double track: {selectedRoute.parallel_group}
              </div>
            )}
          </div>
        ) : selectedCity ? (
          <div className="space-y-3">
            <div className="flex items-center space-x-2 pb-2 border-b border-slate-800">
              <MapPin className="w-4 h-4 text-amber-400" />
              <span className="text-sm font-bold text-white">
                {selectedCity.name}
              </span>
              <span className="text-xs font-mono text-slate-400">
                ({selectedCity.id})
              </span>
            </div>

            <div>
              <span className="text-xs text-slate-400 block mb-1.5 font-medium">
                Connected Routes ({connectedRoutes.length}):
              </span>
              <div className="space-y-1 max-h-44 overflow-y-auto pr-1">
                {connectedRoutes.map((r) => {
                  const targetCityId = r.a === selectedCityId ? r.b : r.a
                  const targetCity = CITY_COORDINATES[targetCityId]
                  return (
                    <div
                      key={r.id}
                      onClick={() => onSelectCity(targetCityId)}
                      className="px-2.5 py-1.5 rounded-lg bg-slate-950/60 border border-slate-800/80 hover:border-amber-500/40 text-xs flex items-center justify-between cursor-pointer transition-colors"
                    >
                      <span className="text-slate-300">
                        → {targetCity?.name}
                      </span>
                      <span className="text-[11px] font-mono text-amber-400">
                        {r.length} w • +{SCORING_TABLE[r.length]} pts
                      </span>
                    </div>
                  )
                })}
              </div>
            </div>
          </div>
        ) : (
          <p className="text-xs text-slate-400 py-3 text-center">
            Click any city node or route on the map to inspect its connections, lengths, and point values.
          </p>
        )}
      </div>

      {/* Scoring Table Reference */}
      <div className="p-4 rounded-2xl bg-slate-900 border border-slate-800 shadow-xl backdrop-blur-md">
        <h3 className="text-xs font-bold uppercase tracking-wider text-slate-400 mb-2.5">
          Official Scoring Table
        </h3>
        <div className="grid grid-cols-3 gap-1.5 text-center text-xs">
          {Object.entries(SCORING_TABLE).map(([length, pts]) => (
            <div
              key={length}
              className="p-1.5 rounded-lg bg-slate-950/60 border border-slate-800/80"
            >
              <div className="text-slate-400 text-[10px]">{length} wagons</div>
              <div className="font-bold text-amber-400">+{pts} pts</div>
            </div>
          ))}
        </div>
      </div>

      {/* Ruleset & Graph Metadata */}
      {catalog && (
        <div className="p-4 rounded-2xl bg-slate-900 border border-slate-800 shadow-xl backdrop-blur-md text-xs space-y-2">
          <h3 className="font-bold uppercase tracking-wider text-slate-400 text-[11px]">
            Ruleset Metadata
          </h3>
          <div className="flex justify-between text-slate-300">
            <span className="text-slate-500">Version:</span>
            <span className="font-mono text-amber-400">{catalog.ruleset_version}</span>
          </div>
          <div className="flex justify-between text-slate-300">
            <span className="text-slate-500">Total Cities:</span>
            <span>{catalog.cities.length}</span>
          </div>
          <div className="flex justify-between text-slate-300">
            <span className="text-slate-500">Route Edges:</span>
            <span>{catalog.routes.length} (11 double tracks)</span>
          </div>
          <div className="flex justify-between text-slate-300">
            <span className="text-slate-500">Catalog Hash:</span>
            <span className="font-mono text-[10px] text-slate-400">
              {catalog.catalog_hash.slice(0, 12)}...
            </span>
          </div>
        </div>
      )}
    </aside>
  )
}

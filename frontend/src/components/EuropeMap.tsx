import React, { useState } from 'react'
import type { Route, RouteColor } from '../types/game'
import { CITY_COORDINATES } from '../data/cityCoordinates'

interface EuropeMapProps {
  routes: Route[]
  selectedCityId: string | null
  onSelectCity: (cityId: string | null) => void
  onSelectRoute: (route: Route | null) => void
}

const COLOR_MAP: Record<RouteColor, string> = {
  PURPLE: '#a855f7',
  PINK: '#ec4899',
  WHITE: '#e2e8f0',
  BLUE: '#3b82f6',
  YELLOW: '#eab308',
  ORANGE: '#f97316',
  BLACK: '#1e293b',
  RED: '#ef4444',
  GREEN: '#22c55e',
  GRAY: '#64748b',
}

export const EuropeMap: React.FC<EuropeMapProps> = ({
  routes,
  selectedCityId,
  onSelectCity,
  onSelectRoute,
}) => {
  const [hoveredRoute, setHoveredRoute] = useState<Route | null>(null)
  const [hoveredCity, setHoveredCity] = useState<string | null>(null)

  // Track double routes and offset them visually
  const parallelGroupsSeen: Record<string, number> = {}

  return (
    <div className="relative w-full rounded-2xl bg-slate-900/90 border border-slate-800 shadow-2xl overflow-hidden backdrop-blur-md">
      {/* SVG Canvas */}
      <svg
        viewBox="0 0 1240 850"
        className="w-full h-auto select-none"
        style={{ minHeight: '520px' }}
      >
        <defs>
          <filter id="city-glow" x="-20%" y="-20%" width="140%" height="140%">
            <feDropShadow dx="0" dy="0" stdDeviation="3" floodColor="#f59e0b" floodOpacity="0.6" />
          </filter>
          <filter id="route-hover-glow" x="-30%" y="-30%" width="160%" height="160%">
            <feDropShadow dx="0" dy="0" stdDeviation="4" floodColor="#ffffff" floodOpacity="0.5" />
          </filter>
        </defs>

        {/* Background Grid Pattern */}
        <g stroke="#1e293b" strokeWidth="0.5" opacity="0.4">
          {Array.from({ length: 25 }).map((_, i) => (
            <line key={`v-${i}`} x1={i * 50} y1="0" x2={i * 50} y2="850" />
          ))}
          {Array.from({ length: 18 }).map((_, i) => (
            <line key={`h-${i}`} x1="0" y1={i * 50} x2="1240" y2={i * 50} />
          ))}
        </g>

        {/* Routes */}
        <g id="routes-layer">
          {routes.map((route) => {
            const coordA = CITY_COORDINATES[route.a]
            const coordB = CITY_COORDINATES[route.b]

            if (!coordA || !coordB) return null

            let { x: x1, y: y1 } = coordA
            let { x: x2, y: y2 } = coordB

            // Calculate parallel offset if part of a double route group
            if (route.parallel_group) {
              const count = parallelGroupsSeen[route.parallel_group] || 0
              parallelGroupsSeen[route.parallel_group] = count + 1

              const dx = x2 - x1
              const dy = y2 - y1
              const len = Math.sqrt(dx * dx + dy * dy)
              const nx = -dy / len
              const ny = dx / len

              const offsetDistance = count === 0 ? -4.5 : 4.5
              x1 += nx * offsetDistance
              y1 += ny * offsetDistance
              x2 += nx * offsetDistance
              y2 += ny * offsetDistance
            }

            const isHighlighted =
              selectedCityId === route.a ||
              selectedCityId === route.b ||
              hoveredCity === route.a ||
              hoveredCity === route.b ||
              hoveredRoute?.id === route.id

            const strokeColor = COLOR_MAP[route.color] || '#64748b'

            return (
              <g
                key={route.id}
                className="cursor-pointer transition-all duration-150"
                onMouseEnter={() => {
                  setHoveredRoute(route)
                  onSelectRoute(route)
                }}
                onMouseLeave={() => {
                  setHoveredRoute(null)
                  onSelectRoute(null)
                }}
                onClick={() => onSelectRoute(route)}
              >
                {/* Thick invisible hit area for easier clicking/hovering */}
                <line
                  x1={x1}
                  y1={y1}
                  x2={x2}
                  y2={y2}
                  stroke="transparent"
                  strokeWidth="18"
                />

                {/* Base Route Line */}
                <line
                  x1={x1}
                  y1={y1}
                  x2={x2}
                  y2={y2}
                  stroke={strokeColor}
                  strokeWidth={isHighlighted ? 6.5 : 4.5}
                  strokeLinecap="round"
                  strokeDasharray={route.source_kind === 'TUNNEL' ? '8,4' : undefined}
                  className="transition-all"
                  filter={isHighlighted ? 'url(#route-hover-glow)' : undefined}
                />

                {/* Ferry Marker Dots if ferry locomotive required */}
                {route.source_locomotives > 0 && (
                  <circle
                    cx={(x1 + x2) / 2}
                    cy={(y1 + y2) / 2}
                    r={3.5}
                    fill="#38bdf8"
                    stroke="#0f172a"
                    strokeWidth="1.5"
                  />
                )}
              </g>
            )
          })}
        </g>

        {/* Cities */}
        <g id="cities-layer">
          {Object.values(CITY_COORDINATES).map((city) => {
            const isSelected = selectedCityId === city.id
            const isHovered = hoveredCity === city.id
            const isConnectedToHover =
              hoveredRoute?.a === city.id || hoveredRoute?.b === city.id

            const radius = isSelected || isHovered || isConnectedToHover ? 8 : 5.5

            return (
              <g
                key={city.id}
                className="cursor-pointer group"
                onClick={() =>
                  onSelectCity(selectedCityId === city.id ? null : city.id)
                }
                onMouseEnter={() => setHoveredCity(city.id)}
                onMouseLeave={() => setHoveredCity(null)}
              >
                {/* Outer Glow on hover / select */}
                {(isSelected || isHovered || isConnectedToHover) && (
                  <circle
                    cx={city.x}
                    cy={city.y}
                    r={radius + 5}
                    fill="none"
                    stroke="#f59e0b"
                    strokeWidth="2"
                    opacity="0.8"
                    filter="url(#city-glow)"
                  />
                )}

                {/* City Node */}
                <circle
                  cx={city.x}
                  cy={city.y}
                  r={radius}
                  fill={isSelected ? '#f59e0b' : '#0f172a'}
                  stroke={isSelected ? '#ffffff' : '#e2e8f0'}
                  strokeWidth={isSelected ? '2.5' : '1.8'}
                  className="transition-all duration-150"
                />

                {/* City Label */}
                <text
                  x={city.x + (city.labelOffset?.x ?? 8)}
                  y={city.y + (city.labelOffset?.y ?? 4)}
                  fill={isSelected || isHovered ? '#fbbf24' : '#cbd5e1'}
                  fontSize={isSelected || isHovered ? '12px' : '10.5px'}
                  fontWeight={isSelected || isHovered ? 'bold' : '500'}
                  className="pointer-events-none select-none transition-all drop-shadow-sm"
                >
                  {city.name}
                </text>
              </g>
            )
          })}
        </g>
      </svg>
    </div>
  )
}

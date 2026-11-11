export type RouteColor =
  | 'PURPLE'
  | 'PINK'
  | 'WHITE'
  | 'BLUE'
  | 'YELLOW'
  | 'ORANGE'
  | 'BLACK'
  | 'RED'
  | 'GREEN'
  | 'GRAY'

export type RouteKind = 'NORMAL' | 'TUNNEL' | 'FERRY'

export interface City {
  id: string
  name: string
}

export interface Route {
  id: string
  a: string
  b: string
  length: number
  color: RouteColor
  parallel_group?: string
  source_kind: RouteKind
  source_locomotives: number
}

export interface PublicCatalogRules {
  tunnels: boolean
  ferries: boolean
  stations: boolean
}

export interface PublicCatalogResponse {
  schema_version: number
  ruleset_version: string
  catalog_hash: string
  rules: PublicCatalogRules
  scoring_table?: Record<number, number>
  ticket_counts?: {
    total: number
    long: number
    regular: number
  }
  cities: City[]
  routes: Route[]
}

export interface UserPrincipal {
  user_id: string
  is_anonymous: boolean
  expires_at: string
}

export interface BackendHealth {
  live: boolean
  ready: boolean
}

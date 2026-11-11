// Package catalog loads and validates the immutable Europe core multigraph.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"ticket-to-ride/assets"
)

const RulesetVersion = "europe-core-v1"

type CityID string
type RouteID string
type TicketID string
type RouteColor string
type RouteKind string

type City struct {
	ID   CityID `json:"id"`
	Name string `json:"name"`
}

type Route struct {
	ID                RouteID    `json:"id"`
	A                 CityID     `json:"a"`
	B                 CityID     `json:"b"`
	Length            int        `json:"length"`
	Color             RouteColor `json:"color"`
	ParallelGroup     string     `json:"parallel_group"`
	SourceKind        RouteKind  `json:"source_kind"`
	SourceLocomotives int        `json:"source_locomotives"`
}

type Ticket struct {
	ID     TicketID `json:"id"`
	A      CityID   `json:"a"`
	B      CityID   `json:"b"`
	Points int      `json:"points"`
	Long   bool     `json:"long"`
}

type Manifest struct {
	SchemaVersion      int    `json:"schema_version"`
	RulesetVersion     string `json:"ruleset_version"`
	SHA256             string `json:"sha256"`
	CityCount          int    `json:"city_count"`
	RouteCount         int    `json:"route_count"`
	TicketCount        int    `json:"ticket_count"`
	LongTicketCount    int    `json:"long_ticket_count"`
	ParallelGroupCount int    `json:"parallel_group_count"`
}

type document struct {
	SchemaVersion  int      `json:"schema_version"`
	RulesetVersion string   `json:"ruleset_version"`
	Cities         []City   `json:"cities"`
	Routes         []Route  `json:"routes"`
	Tickets        []Ticket `json:"tickets"`
}

// Catalog owns all indexes. Getters return values/copies; callers cannot mutate it.
type Catalog struct {
	manifest    Manifest
	cities      []City
	routes      []Route
	tickets     []Ticket
	cityIndex   map[CityID]City
	routeIndex  map[RouteID]Route
	ticketIndex map[TicketID]Ticket
	adjacency   map[CityID][]RouteID
}

func LoadDefault() (*Catalog, error) { return Load(assets.Europe, RulesetVersion) }

func Load(fsys fs.FS, directory string) (*Catalog, error) {
	raw, err := readBounded(fsys, path.Join(directory, "catalog.json"))
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	manifestRaw, err := readBounded(fsys, path.Join(directory, "catalog-manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := decode(manifestRaw, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.SchemaVersion != 1 || m.RulesetVersion != RulesetVersion || m.CityCount != 47 || m.RouteCount != 101 || m.TicketCount != 46 || m.LongTicketCount != 6 || m.ParallelGroupCount != 11 {
		return nil, fmt.Errorf("unsupported catalog manifest")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != m.SHA256 {
		return nil, fmt.Errorf("catalog SHA-256 mismatch")
	}
	var d document
	if err := decode(raw, &d); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if d.SchemaVersion != m.SchemaVersion || d.RulesetVersion != m.RulesetVersion || len(d.Cities) != m.CityCount || len(d.Routes) != m.RouteCount || len(d.Tickets) != m.TicketCount {
		return nil, fmt.Errorf("catalog version or counts mismatch")
	}
	c := &Catalog{manifest: m, cities: d.Cities, routes: d.Routes, tickets: d.Tickets,
		cityIndex: make(map[CityID]City), routeIndex: make(map[RouteID]Route), ticketIndex: make(map[TicketID]Ticket), adjacency: make(map[CityID][]RouteID)}
	if err := c.validate(); err != nil {
		return nil, err
	}
	slices.SortFunc(c.cities, func(a, b City) int { return strings.Compare(string(a.ID), string(b.ID)) })
	slices.SortFunc(c.routes, func(a, b Route) int { return strings.Compare(string(a.ID), string(b.ID)) })
	slices.SortFunc(c.tickets, func(a, b Ticket) int { return strings.Compare(string(a.ID), string(b.ID)) })
	for _, ids := range c.adjacency {
		slices.Sort(ids)
	}
	return c, nil
}

func readBounded(fsys fs.FS, name string) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const limit = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("catalog file too large")
	}
	return raw, nil
}

func decode(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,119}$`)

func (c *Catalog) validate() error {
	for _, city := range c.cities {
		if !idPattern.MatchString(string(city.ID)) || strings.TrimSpace(city.Name) == "" {
			return fmt.Errorf("invalid city %q", city.ID)
		}
		if _, exists := c.cityIndex[city.ID]; exists {
			return fmt.Errorf("duplicate city %q", city.ID)
		}
		c.cityIndex[city.ID] = city
	}
	pairs := make(map[string][]Route)
	groups := make(map[string]string)
	for _, route := range c.routes {
		if !idPattern.MatchString(string(route.ID)) {
			return fmt.Errorf("invalid route ID")
		}
		if _, exists := c.routeIndex[route.ID]; exists {
			return fmt.Errorf("duplicate route %q", route.ID)
		}
		if !c.validEnds(route.A, route.B) {
			return fmt.Errorf("unknown or identical endpoints on route %q", route.ID)
		}
		if _, ok := RoutePoints(route.Length); !ok {
			return fmt.Errorf("unsupported route length on %q", route.ID)
		}
		if !slices.Contains([]RouteColor{"PINK", "WHITE", "BLUE", "YELLOW", "ORANGE", "BLACK", "RED", "GREEN", "GRAY"}, route.Color) {
			return fmt.Errorf("invalid route color on %q", route.ID)
		}
		switch route.SourceKind {
		case "NORMAL", "TUNNEL":
			if route.SourceLocomotives != 0 {
				return fmt.Errorf("non-ferry locomotive metadata on %q", route.ID)
			}
		case "FERRY":
			if route.SourceLocomotives < 1 || route.SourceLocomotives > route.Length {
				return fmt.Errorf("invalid ferry metadata on %q", route.ID)
			}
		default:
			return fmt.Errorf("invalid route kind on %q", route.ID)
		}
		a, b := route.A, route.B
		if a > b {
			a, b = b, a
		}
		pair := string(a) + "/" + string(b)
		pairs[pair] = append(pairs[pair], route)
		c.routeIndex[route.ID] = route
		c.adjacency[route.A] = append(c.adjacency[route.A], route.ID)
		c.adjacency[route.B] = append(c.adjacency[route.B], route.ID)
	}
	for pair, routes := range pairs {
		if len(routes) == 1 {
			if routes[0].ParallelGroup != "" {
				return fmt.Errorf("unpaired parallel route")
			}
			continue
		}
		if len(routes) != 2 || routes[0].ParallelGroup == "" || routes[0].ParallelGroup != routes[1].ParallelGroup || routes[0].Length != routes[1].Length {
			return fmt.Errorf("invalid parallel pair %s", pair)
		}
		group := routes[0].ParallelGroup
		if !idPattern.MatchString(group) {
			return fmt.Errorf("invalid parallel group ID")
		}
		if previous, exists := groups[group]; exists && previous != pair {
			return fmt.Errorf("parallel group spans endpoints")
		}
		groups[group] = pair
	}
	if len(groups) != c.manifest.ParallelGroupCount {
		return fmt.Errorf("parallel group count mismatch")
	}
	long := 0
	for _, ticket := range c.tickets {
		if !idPattern.MatchString(string(ticket.ID)) || !c.validEnds(ticket.A, ticket.B) || ticket.Points <= 0 {
			return fmt.Errorf("invalid ticket %q", ticket.ID)
		}
		if _, exists := c.ticketIndex[ticket.ID]; exists {
			return fmt.Errorf("duplicate ticket %q", ticket.ID)
		}
		if !c.Connected(ticket.A, ticket.B) {
			return fmt.Errorf("unreachable ticket %q", ticket.ID)
		}
		c.ticketIndex[ticket.ID] = ticket
		if ticket.Long {
			long++
		}
	}
	if long != c.manifest.LongTicketCount {
		return fmt.Errorf("long ticket count mismatch")
	}
	// Require every city, including any city not appearing on a ticket, to be reachable.
	for _, city := range c.cities {
		if !c.Connected(c.cities[0].ID, city.ID) {
			return fmt.Errorf("disconnected city %q", city.ID)
		}
	}
	return nil
}

func (c *Catalog) validEnds(a, b CityID) bool {
	_, aok := c.cityIndex[a]
	_, bok := c.cityIndex[b]
	return aok && bok && a != b
}

func (c *Catalog) Manifest() Manifest                { return c.manifest }
func (c *Catalog) Cities() []City                    { return slices.Clone(c.cities) }
func (c *Catalog) Routes() []Route                   { return slices.Clone(c.routes) }
func (c *Catalog) Tickets() []Ticket                 { return slices.Clone(c.tickets) }
func (c *Catalog) Route(id RouteID) (Route, bool)    { r, ok := c.routeIndex[id]; return r, ok }
func (c *Catalog) Ticket(id TicketID) (Ticket, bool) { t, ok := c.ticketIndex[id]; return t, ok }
func (c *Catalog) Adjacent(id CityID) []RouteID      { return slices.Clone(c.adjacency[id]) }

func (c *Catalog) CheckCompatibility(ruleset, hash string) error {
	if ruleset != c.manifest.RulesetVersion || hash != c.manifest.SHA256 {
		return fmt.Errorf("incompatible snapshot ruleset/catalog")
	}
	return nil
}

func (c *Catalog) Connected(a, b CityID) bool {
	if _, ok := c.cityIndex[a]; !ok {
		return false
	}
	if _, ok := c.cityIndex[b]; !ok {
		return false
	}
	seen := map[CityID]bool{a: true}
	queue := []CityID{a}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == b {
			return true
		}
		for _, id := range c.adjacency[current] {
			r := c.routeIndex[id]
			next := r.A
			if next == current {
				next = r.B
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// SimplifiedMapResponse is the public DTO projection served by the map endpoint.
type SimplifiedMapResponse struct {
	SchemaVersion  int             `json:"schema_version"`
	RulesetVersion string          `json:"ruleset_version"`
	CatalogHash    string          `json:"catalog_hash"`
	Rules          RulesetFeatures `json:"rules"`
	Cities         []City          `json:"cities"`
	Routes         []Route         `json:"routes"`
	ScoringTable   map[int]int     `json:"scoring_table"`
	TicketCounts   TicketCounts    `json:"ticket_counts"`
}

// RulesetFeatures describes active rules, independently of the source board metadata.
type RulesetFeatures struct {
	Tunnels  bool `json:"tunnels"`
	Ferries  bool `json:"ferries"`
	Stations bool `json:"stations"`
}

type TicketCounts struct {
	Total   int `json:"total"`
	Long    int `json:"long"`
	Regular int `json:"regular"`
}

// SimplifiedMap generates the DTO representation for public display.
func (c *Catalog) SimplifiedMap() SimplifiedMapResponse {
	points := make(map[int]int)
	for length := 1; length <= 8; length++ {
		if score, ok := RoutePoints(length); ok {
			points[length] = score
		}
	}
	return SimplifiedMapResponse{
		SchemaVersion:  c.manifest.SchemaVersion,
		RulesetVersion: c.manifest.RulesetVersion,
		CatalogHash:    c.manifest.SHA256,
		Rules:          RulesetFeatures{Tunnels: false, Ferries: false, Stations: false},
		Cities:         c.Cities(),
		Routes:         c.Routes(),
		ScoringTable:   points,
		TicketCounts: TicketCounts{
			Total:   c.manifest.TicketCount,
			Long:    c.manifest.LongTicketCount,
			Regular: c.manifest.TicketCount - c.manifest.LongTicketCount,
		},
	}
}

func RoutePoints(length int) (int, bool) {
	switch length {
	case 1:
		return 1, true
	case 2:
		return 2, true
	case 3:
		return 4, true
	case 4:
		return 7, true
	case 5:
		return 10, true
	case 6:
		return 15, true
	case 8:
		return 21, true
	default:
		return 0, false
	}
}

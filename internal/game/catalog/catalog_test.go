package catalog_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"ticket-to-ride/assets"
	"ticket-to-ride/internal/game/catalog"
)

func TestLoadDefault(t *testing.T) {
	t.Parallel()

	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatalf("failed to load canonical Europe catalog: %v", err)
	}

	t.Run("counts and version match base Europe specification", func(t *testing.T) {
		t.Parallel()
		m := cat.Manifest()
		if m.RulesetVersion != "europe-core-v1" {
			t.Errorf("expected europe-core-v1, got %q", m.RulesetVersion)
		}
		if len(cat.Cities()) != 47 {
			t.Errorf("expected 47 cities, got %d", len(cat.Cities()))
		}
		if len(cat.Routes()) != 101 {
			t.Errorf("expected 101 routes, got %d", len(cat.Routes()))
		}
		if len(cat.Tickets()) != 46 {
			t.Errorf("expected 46 tickets, got %d", len(cat.Tickets()))
		}
		if m.LongTicketCount != 6 {
			t.Errorf("expected 6 long tickets, got %d", m.LongTicketCount)
		}
		if m.ParallelGroupCount != 11 {
			t.Errorf("expected 11 parallel groups, got %d", m.ParallelGroupCount)
		}
	})

	t.Run("parallel double-route symmetry and pairing", func(t *testing.T) {
		t.Parallel()
		groups := make(map[string][]catalog.Route)
		for _, r := range cat.Routes() {
			if r.ParallelGroup != "" {
				groups[r.ParallelGroup] = append(groups[r.ParallelGroup], r)
			}
		}
		if len(groups) != 11 {
			t.Fatalf("expected 11 parallel groups, got %d", len(groups))
		}
		for groupName, routes := range groups {
			if len(routes) != 2 {
				t.Fatalf("group %s has %d routes, expected 2", groupName, len(routes))
			}
			r1, r2 := routes[0], routes[1]
			if r1.Length != r2.Length {
				t.Errorf("group %s length mismatch: %d != %d", groupName, r1.Length, r2.Length)
			}
			endpointsMatch := (r1.A == r2.A && r1.B == r2.B) || (r1.A == r2.B && r1.B == r2.A)
			if !endpointsMatch {
				t.Errorf("group %s endpoints do not match: (%s, %s) vs (%s, %s)", groupName, r1.A, r1.B, r2.A, r2.B)
			}
		}
	})

	t.Run("all route scoring points match standard table", func(t *testing.T) {
		t.Parallel()
		for _, r := range cat.Routes() {
			pts, ok := catalog.RoutePoints(r.Length)
			if !ok {
				t.Fatalf("unsupported length %d for route %s", r.Length, r.ID)
			}
			if pts <= 0 {
				t.Errorf("route %s points must be positive, got %d", r.ID, pts)
			}
		}
	})

	t.Run("all destination tickets are connected in the graph", func(t *testing.T) {
		t.Parallel()
		longCount := 0
		for _, ticket := range cat.Tickets() {
			if ticket.Long {
				longCount++
			}
			if ticket.Points <= 0 {
				t.Errorf("ticket %s has non-positive points %d", ticket.ID, ticket.Points)
			}
			if !cat.Connected(ticket.A, ticket.B) {
				t.Errorf("ticket %s endpoints %s and %s are not connected", ticket.ID, ticket.A, ticket.B)
			}
		}
		if longCount != 6 {
			t.Errorf("expected 6 long tickets, got %d", longCount)
		}
	})

	t.Run("adjacency multigraph integrity", func(t *testing.T) {
		t.Parallel()
		for _, city := range cat.Cities() {
			adj := cat.Adjacent(city.ID)
			if len(adj) == 0 {
				t.Errorf("city %s has no adjacent routes", city.ID)
			}
			for _, routeID := range adj {
				r, ok := cat.Route(routeID)
				if !ok {
					t.Fatalf("adjacent route %s not found in catalog", routeID)
				}
				if r.A != city.ID && r.B != city.ID {
					t.Errorf("city %s has route %s in adjacency list which does not connect to it", city.ID, r.ID)
				}
			}
		}
	})

	t.Run("immutability of catalog getters", func(t *testing.T) {
		t.Parallel()
		cities := cat.Cities()
		cities[0].Name = "MutatedName"
		if cat.Cities()[0].Name == "MutatedName" {
			t.Error("catalog cities slice is mutable from outside")
		}

		routes := cat.Routes()
		routes[0].Length = 999
		if cat.Routes()[0].Length == 999 {
			t.Error("catalog routes slice is mutable from outside")
		}

		tickets := cat.Tickets()
		tickets[0].Points = 999
		if cat.Tickets()[0].Points == 999 {
			t.Error("catalog tickets slice is mutable from outside")
		}

		adj := cat.Adjacent(cities[0].ID)
		adj[0] = "mutated-route"
		if cat.Adjacent(cities[0].ID)[0] == "mutated-route" {
			t.Error("catalog adjacency slice is mutable from outside")
		}
	})

	t.Run("check compatibility", func(t *testing.T) {
		t.Parallel()
		m := cat.Manifest()
		if err := cat.CheckCompatibility(m.RulesetVersion, m.SHA256); err != nil {
			t.Errorf("expected compatibility check to succeed, got %v", err)
		}
		if err := cat.CheckCompatibility("wrong-ruleset", m.SHA256); err == nil {
			t.Error("expected compatibility check to fail on wrong ruleset")
		}
		if err := cat.CheckCompatibility(m.RulesetVersion, "wrong-hash"); err == nil {
			t.Error("expected compatibility check to fail on wrong hash")
		}
	})

	t.Run("simplified map DTO serialization", func(t *testing.T) {
		t.Parallel()
		dto := cat.SimplifiedMap()
		if dto.RulesetVersion != "europe-core-v1" {
			t.Errorf("expected europe-core-v1, got %s", dto.RulesetVersion)
		}
		if len(dto.Cities) != 47 || len(dto.Routes) != 101 {
			t.Errorf("unexpected counts in simplified map: %d cities, %d routes", len(dto.Cities), len(dto.Routes))
		}
		if dto.TicketCounts.Total != 46 || dto.TicketCounts.Long != 6 || dto.TicketCounts.Regular != 40 {
			t.Errorf("unexpected ticket counts: %+v", dto.TicketCounts)
		}
	})
}

func TestLoadValidationErrors(t *testing.T) {
	t.Parallel()

	// Load valid raw files from embedded assets
	validCatalogBytes, err := assets.Europe.ReadFile("europe-core-v1/catalog.json")
	if err != nil {
		t.Fatalf("reading valid catalog.json: %v", err)
	}
	validManifestBytes, err := assets.Europe.ReadFile("europe-core-v1/catalog-manifest.json")
	if err != nil {
		t.Fatalf("reading valid catalog-manifest.json: %v", err)
	}

	buildMock := func(tamperCatalog func(map[string]any), tamperManifest func(map[string]any)) fstest.MapFS {
		var catDoc map[string]any
		if err := json.Unmarshal(validCatalogBytes, &catDoc); err != nil {
			panic(err)
		}
		if tamperCatalog != nil {
			tamperCatalog(catDoc)
		}
		rawCat, err := json.Marshal(catDoc)
		if err != nil {
			panic(err)
		}

		var manDoc map[string]any
		if err := json.Unmarshal(validManifestBytes, &manDoc); err != nil {
			panic(err)
		}
		sum := sha256.Sum256(rawCat)
		manDoc["sha256"] = hex.EncodeToString(sum[:])
		if tamperManifest != nil {
			tamperManifest(manDoc)
		}
		rawMan, err := json.Marshal(manDoc)
		if err != nil {
			panic(err)
		}

		return fstest.MapFS{
			"test/catalog.json":          {Data: rawCat},
			"test/catalog-manifest.json": {Data: rawMan},
		}
	}

	t.Run("hash mismatch rejected", func(t *testing.T) {
		t.Parallel()
		mock := buildMock(nil, func(m map[string]any) {
			m["sha256"] = "0000000000000000000000000000000000000000000000000000000000000000"
		})
		_, err := catalog.Load(mock, "test")
		if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Fatalf("expected SHA-256 mismatch error, got %v", err)
		}
	})

	t.Run("self-loop route rejected", func(t *testing.T) {
		t.Parallel()
		mock := buildMock(func(d map[string]any) {
			routes := d["routes"].([]any)
			first := routes[0].(map[string]any)
			first["b"] = first["a"] // create self-loop
		}, nil)
		_, err := catalog.Load(mock, "test")
		if err == nil || !strings.Contains(err.Error(), "identical endpoints") {
			t.Fatalf("expected identical endpoints error, got %v", err)
		}
	})

	t.Run("unsupported route length rejected", func(t *testing.T) {
		t.Parallel()
		var doc map[string]any
		_ = json.Unmarshal(validCatalogBytes, &doc)
		routes := doc["routes"].([]any)
		routes[0].(map[string]any)["length"] = 7
		raw, _ := json.Marshal(doc)
		sum := sha256.Sum256(raw)
		mfs := fstest.MapFS{
			"test/catalog.json": {Data: raw},
			"test/catalog-manifest.json": {
				Data: []byte(`{"schema_version":1,"ruleset_version":"europe-core-v1","sha256":"` + hex.EncodeToString(sum[:]) + `","city_count":47,"route_count":101,"ticket_count":46,"long_ticket_count":6,"parallel_group_count":11}`),
			},
		}
		_, err := catalog.Load(mfs, "test")
		if err == nil || !strings.Contains(err.Error(), "unsupported route length") {
			t.Fatalf("expected unsupported route length error, got %v", err)
		}
	})

	t.Run("unpaired parallel route rejected", func(t *testing.T) {
		t.Parallel()
		var doc map[string]any
		_ = json.Unmarshal(validCatalogBytes, &doc)
		routes := doc["routes"].([]any)
		// Change parallel group of one route
		routes[0].(map[string]any)["parallel_group"] = "new-lone-group"
		raw, _ := json.Marshal(doc)
		sum := sha256.Sum256(raw)
		mfs := fstest.MapFS{
			"test/catalog.json": {Data: raw},
			"test/catalog-manifest.json": {
				Data: []byte(`{"schema_version":1,"ruleset_version":"europe-core-v1","sha256":"` + hex.EncodeToString(sum[:]) + `","city_count":47,"route_count":101,"ticket_count":46,"long_ticket_count":6,"parallel_group_count":11}`),
			},
		}
		_, err := catalog.Load(mfs, "test")
		if err == nil || !strings.Contains(err.Error(), "unpaired parallel route") {
			t.Fatalf("expected unpaired parallel route error, got %v", err)
		}
	})
}

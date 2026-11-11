package catalog

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"testing/fstest"
)

func TestReviewedBoardCorrections(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     RouteID
		length int
		color  RouteColor
		kind   RouteKind
		locos  int
	}{
		{"berlin--danzig--1", 4, "GRAY", "NORMAL", 0},
		{"palermo--smyrna--1", 6, "GRAY", "FERRY", 2},
		{"marseille--zurich--1", 2, "PINK", "TUNNEL", 0},
		{"petrograd--stockholm--1", 8, "GRAY", "TUNNEL", 0},
		{"essen--kobenhavn--1", 3, "GRAY", "FERRY", 1},
		{"essen--kobenhavn--2", 3, "GRAY", "FERRY", 1},
	} {
		r, ok := c.Route(tc.id)
		if !ok || r.Length != tc.length || r.Color != tc.color || r.SourceKind != tc.kind || r.SourceLocomotives != tc.locos {
			t.Errorf("reviewed route %s: %+v", tc.id, r)
		}
	}
	if ticket, ok := c.Ticket("stockholm--wien"); !ok || ticket.Points != 11 || ticket.Long {
		t.Fatalf("incorrect Stockholm-Wien ticket: %+v", ticket)
	}
	if _, exists := c.Route("athina--palermo--1"); exists {
		t.Fatal("incorrect Palermo-Athina route remains")
	}
	for length, want := range map[int]int{1: 1, 2: 2, 3: 4, 4: 7, 5: 10, 6: 15, 8: 21} {
		if got, ok := RoutePoints(length); !ok || got != want {
			t.Errorf("length %d: %d, %v", length, got, ok)
		}
	}
	if _, ok := RoutePoints(7); ok {
		t.Fatal("unsupported route length accepted")
	}
}

func TestPublicMapProjectionIsDetached(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	view := c.SimplifiedMap()
	if view.SchemaVersion != 1 || view.CatalogHash != c.Manifest().SHA256 || view.Rules != (RulesetFeatures{}) {
		t.Fatal("public map has incompatible catalog metadata or rules")
	}
	if view.ScoringTable[5] != 10 || view.TicketCounts.Total != 46 || view.TicketCounts.Regular != 40 || view.TicketCounts.Long != 6 {
		t.Fatal("public scoring table or ticket counts are incorrect")
	}
	if _, exists := view.ScoringTable[7]; exists {
		t.Fatal("public map advertises unsupported length 7")
	}
	view.Cities[0].Name = "changed"
	view.Routes[0].Length = 999
	view.ScoringTable[5] = 999
	next := c.SimplifiedMap()
	if next.Cities[0].Name == "changed" || next.Routes[0].Length == 999 || next.ScoringTable[5] != 10 {
		t.Fatal("public projection mutated shared catalog state")
	}
}

func FuzzCatalogDecoder(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema_version":1}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 2<<20 {
			t.Skip()
		}
		sum := sha256.Sum256(raw)
		manifest := fmt.Sprintf(`{"schema_version":1,"ruleset_version":"europe-core-v1","sha256":"%x","city_count":47,"route_count":101,"ticket_count":46,"long_ticket_count":6,"parallel_group_count":11}`, sum)
		_, _ = Load(fstest.MapFS{"catalog.json": {Data: raw}, "catalog-manifest.json": {Data: []byte(manifest)}}, ".")
	})
}

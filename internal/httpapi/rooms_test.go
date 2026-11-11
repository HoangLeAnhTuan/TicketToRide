package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"ticket-to-ride/internal/auth"
	"ticket-to-ride/internal/game/catalog"
	"ticket-to-ride/internal/repository/memory"
	"ticket-to-ride/internal/room"
	"time"
)

type lobbyVerifier struct{}

func (lobbyVerifier) Warm(context.Context) error { return nil }
func (lobbyVerifier) Verify(_ context.Context, value string) (auth.Principal, error) {
	if !room.ValidID(value) {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	return auth.Principal{UserID: value, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func lobbyID(t *testing.T) string {
	t.Helper()
	id, err := room.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRoomRESTContractAndCORS(t *testing.T) {
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	lobby, err := room.NewService(memory.New(nil), room.Options{Ruleset: catalog.RulesetVersion, Hash: cat.Manifest().SHA256, Instance: lobbyID(t), Workers: 2, Queue: 16, MaxRooms: 20, BcryptCost: 8, OperationTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := lobby.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	api, err := New(cat, lobbyVerifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRooms(lobby), WithAllowedOrigins([]string{"http://localhost:5173"}), RequirePersistence())
	if err != nil {
		t.Fatal(err)
	}
	host, guest, game, key := lobbyID(t), lobbyID(t), lobbyID(t), lobbyID(t)
	call := func(method, path, user, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if user != "" {
			r.Header.Set("Authorization", "Bearer "+user)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	body := `{"room_id":"` + game + `","name":"Room","max_players":2,"display_name":"Host"}`
	w := call("POST", "/v1/rooms", host, key, body)
	if w.Code != 201 {
		t.Fatalf("create %d: %s", w.Code, w.Body.String())
	}
	var created room.Outcome
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/v1/rooms", host, key, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatal("create not idempotent")
	}
	w = call("POST", "/v1/rooms/"+game+"/join", guest, lobbyID(t), `{"display_name":"Guest"}`)
	if w.Code != 200 {
		t.Fatalf("join: %d %s", w.Code, w.Body.String())
	}
	w = call("GET", "/v1/rooms/"+game+"/state", lobbyID(t), "", "")
	if w.Code != 403 {
		t.Fatal("nonmember obtained a state view")
	}
	w = call("PATCH", "/v1/rooms/"+game+"/player", guest, lobbyID(t), `{"expected_version":2,"color":"GREEN"}`)
	if w.Code != 200 {
		t.Fatalf("color: %d %s", w.Code, w.Body.String())
	}
	w = call("PATCH", "/v1/rooms/"+game+"/player", guest, lobbyID(t), `{"expected_version":2,"color":"RED"}`)
	if w.Code != 409 {
		t.Fatal("stale command was accepted")
	}
	w = call("POST", "/v1/rooms/"+game+"/ws-ticket", guest, "", `{}`)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("ticket not protected")
	}
	var ticket room.Ticket
	if err := json.Unmarshal(w.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	if _, err := lobby.ConsumeTicket(ticket.Token, game); err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/v1/rooms/"+game+"/leave", guest, "", `{}`)
	if w.Code != 400 {
		t.Fatal("mutation accepted without idempotency key")
	}
	for _, origin := range []string{"http://localhost:5173", "https://attacker.invalid"} {
		r := httptest.NewRequest("OPTIONS", "/v1/rooms", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if origin == "http://localhost:5173" && w.Code != 204 {
			t.Fatal("frontend preflight failed")
		}
		if origin != "http://localhost:5173" && w.Code != 403 {
			t.Fatal("unapproved origin allowed")
		}
	}
	w = call("GET", "/v1/rooms/by-code/"+created.State.Room.Code, guest, "", "")
	if w.Code != 200 {
		t.Fatal("code lookup failed")
	}
	for range 6 {
		w = call("POST", "/v1/rooms/"+game+"/join", guest, lobbyID(t), `{"display_name":"Guest"}`)
	}
	if w.Code != 429 {
		t.Fatal("password admission rate limit not enforced")
	}
}

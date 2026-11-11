package room_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"ticket-to-ride/internal/repository/memory"
	"ticket-to-ride/internal/room"
)

const (
	testRuleset = "europe-core-v1"
	testHash    = "ef83addf6c7d29e0a3ad97db226df6677d4fdc6d8b024fa3e1b0ec08251bdf44"
)

func newTestService(t *testing.T) (*room.Service, *memory.Store) {
	t.Helper()
	store := memory.New(time.Now)
	instance, err := room.NewID()
	if err != nil {
		t.Fatalf("instance ID: %v", err)
	}
	s, err := room.NewService(store, room.Options{
		Ruleset:          testRuleset,
		Hash:             testHash,
		Instance:         instance,
		Workers:          4,
		Queue:            64,
		MaxRooms:         100,
		BcryptCost:       8,
		OperationTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})
	return s, store
}

func TestRoomLifecycle(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	roomID, _ := room.NewID()
	hostID, _ := room.NewID()
	reqID1, _ := room.NewID()

	// 1. Create Room with password
	out, err := s.Do(ctx, room.Command{
		Kind:        "CREATE",
		RoomID:      roomID,
		UserID:      hostID,
		RequestID:   reqID1,
		Name:        "Orient Express",
		DisplayName: "HostPlayer",
		MaxPlayers:  3,
		Password:    "secret123",
	})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if out.Receipt == nil || out.Receipt.CommittedVersion != 1 {
		t.Fatalf("unexpected receipt: %+v", out.Receipt)
	}
	if out.State.Room.Name != "Orient Express" || out.State.Room.MaxPlayers != 3 || !out.State.Room.HasPassword {
		t.Fatalf("unexpected state: %+v", out.State.Room)
	}
	if len(out.State.Players) != 1 || out.State.Players[0].Seat != 0 {
		t.Fatalf("creator not in seat 0: %+v", out.State.Players)
	}

	// 2. Duplicate Create (idempotent replay)
	dupOut, err := s.Do(ctx, room.Command{
		Kind:        "CREATE",
		RoomID:      roomID,
		UserID:      hostID,
		RequestID:   reqID1,
		Name:        "Orient Express",
		DisplayName: "HostPlayer",
		MaxPlayers:  3,
		Password:    "secret123",
	})
	if err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	if dupOut.Receipt == nil || !dupOut.Receipt.Duplicate {
		t.Fatalf("expected duplicate flag in receipt: %+v", dupOut.Receipt)
	}

	// 3. Join with wrong password
	user2ID, _ := room.NewID()
	reqID2, _ := room.NewID()
	_, err = s.Do(ctx, room.Command{
		Kind:        "JOIN",
		RoomID:      roomID,
		UserID:      user2ID,
		RequestID:   reqID2,
		DisplayName: "PlayerTwo",
		Password:    "wrongpass",
	})
	if err == nil || err.Error() != "INVALID_PASSWORD" {
		t.Fatalf("expected INVALID_PASSWORD, got %v", err)
	}

	// 4. Join with correct password
	joinOut, err := s.Do(ctx, room.Command{
		Kind:        "JOIN",
		RoomID:      roomID,
		UserID:      user2ID,
		RequestID:   reqID2,
		DisplayName: "PlayerTwo",
		Password:    "secret123",
	})
	if err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if len(joinOut.State.Players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(joinOut.State.Players))
	}
	if joinOut.State.Players[1].Seat != 1 {
		t.Fatalf("expected seat 1, got %d", joinOut.State.Players[1].Seat)
	}

	// 5. Connect (ATTACH)
	genHost, _ := room.NewID()
	genP2, _ := room.NewID()
	attachHostReq, _ := room.NewID()
	attachP2Req, _ := room.NewID()

	s.Do(ctx, room.Command{Kind: "ATTACH", RoomID: roomID, UserID: hostID, RequestID: attachHostReq, Generation: genHost})
	s.Do(ctx, room.Command{Kind: "ATTACH", RoomID: roomID, UserID: user2ID, RequestID: attachP2Req, Generation: genP2})

	// 6. Set Colors
	colorReq1, _ := room.NewID()
	colorReq2, _ := room.NewID()
	sOut, err := s.Do(ctx, room.Command{
		Kind:            "SET_COLOR",
		RoomID:          roomID,
		UserID:          hostID,
		RequestID:       colorReq1,
		ExpectedVersion: joinOut.State.StateVersion + 2, // account for ATTACH
		Color:           "BLUE",
	})
	if err != nil {
		t.Fatalf("set color host: %v", err)
	}

	// Player 2 tries duplicate color -> COLOR_TAKEN
	_, err = s.Do(ctx, room.Command{
		Kind:            "SET_COLOR",
		RoomID:          roomID,
		UserID:          user2ID,
		RequestID:       colorReq2,
		ExpectedVersion: sOut.State.StateVersion,
		Color:           "BLUE",
	})
	if err == nil || err.Error() != "COLOR_TAKEN" {
		t.Fatalf("expected COLOR_TAKEN, got %v", err)
	}

	// Player 2 picks RED
	p2ColorOut, err := s.Do(ctx, room.Command{
		Kind:            "SET_COLOR",
		RoomID:          roomID,
		UserID:          user2ID,
		RequestID:       colorReq2,
		ExpectedVersion: sOut.State.StateVersion,
		Color:           "RED",
	})
	if err != nil {
		t.Fatalf("set color player 2: %v", err)
	}

	// 7. Set Ready
	readyReq, _ := room.NewID()
	readyOut, err := s.Do(ctx, room.Command{
		Kind:            "SET_READY",
		RoomID:          roomID,
		UserID:          user2ID,
		RequestID:       readyReq,
		ExpectedVersion: p2ColorOut.State.StateVersion,
		Ready:           true,
	})
	if err != nil {
		t.Fatalf("set ready: %v", err)
	}
	if !readyOut.State.Players[1].IsReady {
		t.Fatalf("expected player 2 to be ready")
	}

	// 8. Host leaves -> host transferred to Player 2
	leaveReq1, _ := room.NewID()
	_, err = s.Do(ctx, room.Command{
		Kind:      "LEAVE",
		RoomID:    roomID,
		UserID:    hostID,
		RequestID: leaveReq1,
	})
	if err != nil {
		t.Fatalf("host leave: %v", err)
	}

	view, err := s.View(ctx, roomID, user2ID)
	if err != nil {
		t.Fatalf("view room after host leave: %v", err)
	}
	if view.Room.HostID != user2ID {
		t.Fatalf("expected host to transfer to user2ID, got %s", view.Room.HostID)
	}

	// 9. Last player leaves -> room closed
	leaveReq2, _ := room.NewID()
	_, err = s.Do(ctx, room.Command{
		Kind:      "LEAVE",
		RoomID:    roomID,
		UserID:    user2ID,
		RequestID: leaveReq2,
	})
	if err != nil {
		t.Fatalf("last leave: %v", err)
	}

	// Room should not be in public list
	page, err := s.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	for _, r := range page.Rooms {
		if r.ID == roomID {
			t.Fatalf("closed room still in list: %s", roomID)
		}
	}
}

func TestConcurrentJoinsRespectCapacity(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	roomID, _ := room.NewID()
	hostID, _ := room.NewID()
	reqID, _ := room.NewID()

	// 3-player room: Host + 2 slots
	_, err := s.Do(ctx, room.Command{
		Kind:        "CREATE",
		RoomID:      roomID,
		UserID:      hostID,
		RequestID:   reqID,
		Name:        "Sprint Track",
		DisplayName: "Host",
		MaxPlayers:  3,
	})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// 10 concurrent players racing for 2 remaining seats
	var wg sync.WaitGroup
	var successCount int
	var mu sync.Mutex

	for i := 1; i <= 10; i++ {
		wg.Add(1)
		go func(playerNum int) {
			defer wg.Done()
			uid, _ := room.NewID()
			rid, _ := room.NewID()
			_, err := s.Do(ctx, room.Command{
				Kind:        "JOIN",
				RoomID:      roomID,
				UserID:      uid,
				RequestID:   rid,
				DisplayName: fmt.Sprintf("Runner-%d", playerNum),
			})
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if successCount != 2 {
		t.Fatalf("expected exactly 2 successful joins into 3-player room, got %d", successCount)
	}

	view, err := s.View(ctx, roomID, hostID)
	if err != nil {
		t.Fatalf("view room: %v", err)
	}
	if len(view.Players) != 3 {
		t.Fatalf("expected 3 players, got %d", len(view.Players))
	}
}

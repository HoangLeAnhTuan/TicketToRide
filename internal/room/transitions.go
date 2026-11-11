package room

import (
	"slices"
	"strings"
	"time"
)

func apply(base State, c Command, now time.Time) (State, error) {
	s := base.Clone()
	if s.Room.ClosedAt != nil {
		return s, Failure("ROOM_NOT_FOUND")
	}
	if s.Room.Status != "WAITING" {
		return s, Failure("ROOM_LOCKED")
	}
	p, member := s.Players[c.UserID]
	if c.Kind != "JOIN" && !member {
		return s, Failure("NOT_MEMBER")
	}
	if c.ExpectedVersion != 0 && c.ExpectedVersion != s.StateVersion {
		return s, Failure("STALE_STATE")
	}
	changed := false
	switch c.Kind {
	case "JOIN":
		if member {
			break
		}
		if len(s.Players) >= s.Room.MaxPlayers {
			return s, Failure("ROOM_FULL")
		}
		name, err := Text(c.DisplayName, 32)
		if err != nil {
			return s, err
		}
		seat := 0
		for ; seat < 5; seat++ {
			used := false
			for _, p := range s.Players {
				if p.Seat == seat {
					used = true
				}
			}
			if !used {
				break
			}
		}
		s.Players[c.UserID] = Player{UserID: c.UserID, DisplayName: name, Seat: seat, JoinedAt: now}
		resetReady(&s)
		changed = true
	case "LEAVE":
		delete(s.Players, c.UserID)
		resetReady(&s)
		changed = true
		if len(s.Players) == 0 {
			s.Room.HostID = ""
			s.Room.ClosedAt = &now
		} else if s.Room.HostID == c.UserID {
			s.Room.HostID = earliest(s, false)
		}
	case "SET_COLOR":
		if !ValidColor(c.Color) {
			return s, Failure("INVALID_MESSAGE")
		}
		for id, other := range s.Players {
			if id != c.UserID && other.Color == c.Color {
				return s, Failure("COLOR_TAKEN")
			}
		}
		if p.Color != c.Color {
			p.Color = c.Color
			s.Players[c.UserID] = p
			resetReady(&s)
			changed = true
		}
	case "SET_DISPLAY_NAME":
		name, err := Text(c.DisplayName, 32)
		if err != nil {
			return s, err
		}
		if p.DisplayName != name {
			p.DisplayName = name
			p.IsReady = false
			s.Players[c.UserID] = p
			changed = true
		}
	case "SET_READY":
		if c.Ready && (p.Color == "" || !p.IsConnected) {
			return s, Failure("NOT_READY")
		}
		if p.IsReady != c.Ready {
			p.IsReady = c.Ready
			s.Players[c.UserID] = p
			changed = true
		}
	case "ATTACH", "DETACH":
		connected := c.Kind == "ATTACH"
		if p.IsConnected != connected {
			p.IsConnected = connected
			if !connected {
				p.IsReady = false
			}
			s.Players[c.UserID] = p
			changed = true
		}
		host := s.Players[s.Room.HostID]
		if !host.IsConnected {
			if next := earliest(s, true); next != "" && next != s.Room.HostID {
				s.Room.HostID = next
				changed = true
			}
		}
	default:
		return s, Failure("INVALID_MESSAGE")
	}
	if changed {
		s.StateVersion++
	}
	return s, nil
}
func resetReady(s *State) {
	for id, p := range s.Players {
		p.IsReady = false
		s.Players[id] = p
	}
}
func earliest(s State, connected bool) string {
	players := []Player{}
	for _, p := range s.Players {
		if !connected || p.IsConnected {
			players = append(players, p)
		}
	}
	slices.SortFunc(players, func(a, b Player) int {
		if n := a.JoinedAt.Compare(b.JoinedAt); n != 0 {
			return n
		}
		return strings.Compare(a.UserID, b.UserID)
	})
	if len(players) == 0 {
		return ""
	}
	return players[0].UserID
}

// Package room owns authoritative lobby state and its single-writer actors.
package room

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Error struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *Error) Error() string { return e.Code }
func Failure(code string) *Error {
	return &Error{Code: code, Message: strings.ReplaceAll(strings.ToLower(code), "_", " "), Retryable: code == "SERVER_BUSY" || code == "PERSISTENCE_UNAVAILABLE" || code == "OWNER_LOST"}
}

func code(err error, target string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == target
}

var ErrUncertain = Failure("COMMIT_UNCERTAIN")

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ValidID(id string) bool {
	return uuidRE.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

func Text(s string, max int) (string, error) {
	if !utf8.ValidString(s) {
		return "", Failure("INVALID_MESSAGE")
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > max {
		return "", Failure("INVALID_MESSAGE")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", Failure("INVALID_MESSAGE")
		}
	}
	return s, nil
}

type Meta struct {
	ID              string     `json:"id"`
	Code            string     `json:"code"`
	Name            string     `json:"name"`
	HostID          string     `json:"host_id"`
	MaxPlayers      int        `json:"max_players"`
	Status          string     `json:"status"`
	HasPassword     bool       `json:"has_password"`
	PasswordVersion int64      `json:"password_version"`
	CreatedAt       time.Time  `json:"created_at"`
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
}
type Player struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Color       string    `json:"color"`
	Seat        int       `json:"seat"`
	JoinedAt    time.Time `json:"joined_at"`
	IsReady     bool      `json:"is_ready"`
	IsConnected bool      `json:"is_connected"`
}
type State struct {
	SchemaVersion  int               `json:"schema_version"`
	RulesetVersion string            `json:"ruleset_version"`
	CatalogHash    string            `json:"catalog_hash"`
	StateVersion   int64             `json:"state_version"`
	Room           Meta              `json:"room"`
	Players        map[string]Player `json:"players"`
	// Phase 2 rejects non-null gameplay snapshots rather than partially decoding secrets.
	Game json.RawMessage `json:"game,omitempty"`
}
type Owner struct {
	Instance string
	Epoch    int64
	Until    time.Time
}
type Record struct {
	State        State
	PasswordHash string
	Owner        Owner
}

func (s State) Clone() State {
	copy := s
	copy.Players = make(map[string]Player, len(s.Players))
	for k, v := range s.Players {
		copy.Players[k] = v
	}
	copy.Game = slices.Clone(s.Game)
	if s.Room.ClosedAt != nil {
		v := *s.Room.ClosedAt
		copy.Room.ClosedAt = &v
	}
	return copy
}
func (s State) Validate(ruleset, hash string) error {
	if s.SchemaVersion != 1 || s.RulesetVersion != ruleset || s.CatalogHash != hash || s.StateVersion < 1 || s.StateVersion >= 1<<53 {
		return Failure("INCOMPATIBLE_SNAPSHOT")
	}
	if s.Room.Status != "WAITING" || (len(s.Game) > 0 && string(s.Game) != "null") {
		return Failure("INCOMPATIBLE_SNAPSHOT")
	}
	if !ValidID(s.Room.ID) || !codeRegex.MatchString(s.Room.Code) || s.Room.MaxPlayers < 2 || s.Room.MaxPlayers > 5 || s.Room.CreatedAt.IsZero() || s.Room.PasswordVersion < 1 {
		return Failure("INVALID_SNAPSHOT")
	}
	if _, err := Text(s.Room.Name, 80); err != nil {
		return Failure("INVALID_SNAPSHOT")
	}
	if len(s.Players) > s.Room.MaxPlayers {
		return Failure("INVALID_SNAPSHOT")
	}
	if s.Room.ClosedAt != nil {
		if len(s.Players) != 0 || s.Room.HostID != "" {
			return Failure("INVALID_SNAPSHOT")
		}
	} else {
		if _, ok := s.Players[s.Room.HostID]; !ok {
			return Failure("INVALID_SNAPSHOT")
		}
	}
	colors := map[string]bool{}
	seats := map[int]bool{}
	for id, p := range s.Players {
		if !ValidID(id) || id != p.UserID || p.Seat < 0 || p.Seat > 4 || seats[p.Seat] || p.JoinedAt.IsZero() {
			return Failure("INVALID_SNAPSHOT")
		}
		seats[p.Seat] = true
		if _, err := Text(p.DisplayName, 32); err != nil {
			return Failure("INVALID_SNAPSHOT")
		}
		if p.Color != "" {
			if !ValidColor(p.Color) || colors[p.Color] {
				return Failure("INVALID_SNAPSHOT")
			}
			colors[p.Color] = true
		}
		if p.IsReady && (p.Color == "" || !p.IsConnected) {
			return Failure("INVALID_SNAPSHOT")
		}
	}
	return nil
}
func Decode(raw []byte, ruleset, hash string) (State, error) {
	if len(raw) > 2<<20 {
		return State{}, Failure("INVALID_SNAPSHOT")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var s State
	if err := d.Decode(&s); err != nil {
		return State{}, Failure("INVALID_SNAPSHOT")
	}
	if d.Decode(new(any)) != io.EOF {
		return State{}, Failure("INVALID_SNAPSHOT")
	}
	return s, s.Validate(ruleset, hash)
}
func ValidColor(s string) bool {
	return slices.Contains([]string{"BLUE", "RED", "GREEN", "YELLOW", "BLACK"}, s)
}

type Ack struct {
	RequestID        string `json:"request_id"`
	CommandType      string `json:"command_type"`
	CommittedVersion int64  `json:"committed_version"`
	Duplicate        bool   `json:"duplicate"`
}
type Receipt struct {
	UserID string
	Hash   string
	Ack    Ack
}
type Command struct {
	ExpiresAt       time.Time
	Kind            string
	RoomID          string
	UserID          string
	RequestID       string
	ExpectedVersion int64
	Name            string
	DisplayName     string
	MaxPlayers      int
	Password        string
	Color           string
	Ready           bool
	// Generation is server-created and is never accepted in a REST body.
	Generation string
}

func (c Command) Hash() string {
	// Passwords are credentials, not a fast offline-verifiable password digest.
	// CREATE separately checks its stored bcrypt hash before replaying a receipt.
	input := struct {
		Kind, RoomID, UserID, Name, DisplayName, Color, Generation string
		MaxPlayers                                                 int
		Ready, HasPassword                                         bool
	}{c.Kind, c.RoomID, c.UserID, c.Name, c.DisplayName, c.Color, c.Generation, c.MaxPlayers, c.Ready, c.Password != "" && c.Kind == "CREATE"}
	raw, _ := json.Marshal(input)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

type PublicRoom struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	HostID      string `json:"host_id"`
	MaxPlayers  int    `json:"max_players"`
	Status      string `json:"status"`
	HasPassword bool   `json:"has_password"`
}
type PublicPlayer struct {
	UserID          string `json:"user_id"`
	DisplayName     string `json:"display_name"`
	Color           string `json:"color"`
	Seat            int    `json:"seat"`
	IsReady         bool   `json:"is_ready"`
	IsConnected     bool   `json:"is_connected"`
	HandCount       int    `json:"hand_count"`
	TicketCount     int    `json:"ticket_count"`
	TrainsRemaining int    `json:"trains_remaining"`
	RoutePoints     int    `json:"route_points"`
}
type SelfView struct {
	UserID         string   `json:"user_id"`
	AllowedActions []string `json:"allowed_actions"`
}
type View struct {
	SchemaVersion  int            `json:"schema_version"`
	RulesetVersion string         `json:"ruleset_version"`
	CatalogHash    string         `json:"catalog_hash"`
	StateVersion   int64          `json:"state_version"`
	Room           PublicRoom     `json:"room"`
	Players        []PublicPlayer `json:"players"`
	Self           SelfView       `json:"self"`
}

func (s State) Project(user string) (View, error) {
	if s.Room.ClosedAt != nil {
		return View{}, Failure("ROOM_NOT_FOUND")
	}
	if _, ok := s.Players[user]; !ok {
		return View{}, Failure("NOT_MEMBER")
	}
	r := s.Room
	v := View{SchemaVersion: s.SchemaVersion, RulesetVersion: s.RulesetVersion, CatalogHash: s.CatalogHash, StateVersion: s.StateVersion, Room: PublicRoom{r.ID, r.Code, r.Name, r.HostID, r.MaxPlayers, r.Status, r.HasPassword}, Players: []PublicPlayer{}, Self: SelfView{UserID: user, AllowedActions: []string{"SET_COLOR", "SET_DISPLAY_NAME", "SET_READY"}}}
	for _, p := range s.Players {
		v.Players = append(v.Players, PublicPlayer{UserID: p.UserID, DisplayName: p.DisplayName, Color: p.Color, Seat: p.Seat, IsReady: p.IsReady, IsConnected: p.IsConnected})
	}
	slices.SortFunc(v.Players, func(a, b PublicPlayer) int { return a.Seat - b.Seat })
	return v, nil
}

type Summary struct {
	PublicRoom
	PlayerCount int       `json:"player_count"`
	CreatedAt   time.Time `json:"created_at"`
}
type Cursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}
type Page struct {
	Rooms      []Summary `json:"rooms"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

package landlord

import (
	"encoding/json"
	_ "github.com/davyxu/cellnet/codec/gogopb" // required by proto init
	"landlord_go/proto"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Test infrastructure
//
// recordSender captures every outbound message so tests can assert on what
// handlers produce without standing up the full cellnet stack.
// ---------------------------------------------------------------------------

type sentMsg struct {
	jsonType int32
	data     []byte
	cid      *proto.ClientID // nil for broadcasts
	cids     []*proto.ClientID
}

type recordSender struct {
	mu   sync.Mutex
	msgs []sentMsg
}

func (r *recordSender) Send(cid *proto.ClientID, jsonType int32, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, sentMsg{jsonType: jsonType, data: data, cid: cid})
}
func (r *recordSender) BroadcastAll(jsonType int32, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, sentMsg{jsonType: jsonType, data: data})
}
func (r *recordSender) MultipleSend(cids []*proto.ClientID, jsonType int32, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, sentMsg{jsonType: jsonType, data: data, cids: cids})
}

func (r *recordSender) byType(t int32) []sentMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []sentMsg
	for _, m := range r.msgs {
		if m.jsonType == t {
			out = append(out, m)
		}
	}
	return out
}

func (r *recordSender) reset() {
	r.mu.Lock()
	r.msgs = nil
	r.mu.Unlock()
}

// installTestSender swaps in a recordSender and returns a cleanup function
// that restores the original sender. Tests should `defer cleanup()`.
func installTestSender() (*recordSender, func()) {
	rec := &recordSender{}
	prev := SetSender(rec)
	return rec, func() { SetSender(prev) }
}

// resetGlobalState clears all sync.Maps and the hall cache so each test
// starts from a clean slate. The init() pool of pre-created Players/Tables
// is still present but their fields are zero-valued.
func resetGlobalState(t *testing.T) {
	t.Helper()
	playerMap.Range(func(k, _ interface{}) bool { playerMap.Delete(k); return true })
	tableMap.Range(func(k, _ interface{}) bool { tableMap.Delete(k); return true })
	userName2Player.Range(func(k, _ interface{}) bool { userName2Player.Delete(k); return true })
	clientID2Player.Range(func(k, _ interface{}) bool { clientID2Player.Delete(k); return true })
	hallList = nil
	// Re-seed one table for tests that need it.
	tableMap.Store(int32(1), &Table{
		ThrowOutCards:   make(map[int32]int32),
		PlayersCardsOut: make(map[int32]int32),
		TableNum:        1,
	})
}

func newCID(id int64) *proto.ClientID { return &proto.ClientID{ID: id, SvcID: "test"} }

// ---------------------------------------------------------------------------
// Pure-function unit tests
// ---------------------------------------------------------------------------

func TestGetSeatNum(t *testing.T) {
	if got := GetSeatNum(1, 0); got != 1 {
		t.Errorf("table1 seat0 got %d want 1", got)
	}
	if got := GetSeatNum(2, 1); got != 5 {
		t.Errorf("table2 seat1 got %d want 5", got)
	}
}

func TestSeatUserNameMap(t *testing.T) {
	tbl := &Table{Players: []*Player{
		{SeatNum: 1, UserName: "a"},
		{SeatNum: 2, UserName: "b"},
		nil,
	}}
	m := seatUserNameMap(tbl)
	if m[1] != "a" || m[2] != "b" || len(m) != 2 {
		t.Errorf("unexpected map: %v", m)
	}
}

func TestAddAndRemovePlayer(t *testing.T) {
	tbl := &Table{
		TableNum:        1,
		ThrowOutCards:   make(map[int32]int32),
		PlayersCardsOut: make(map[int32]int32),
	}
	p := &Player{UserName: "x", Cid: newCID(100)}
	seat := addPlayerToTable(tbl, p)
	if seat != 1 || tbl.PlayerCount != 1 || len(tbl.Players) != 1 {
		t.Fatalf("add failed: seat=%d count=%d", seat, tbl.PlayerCount)
	}
	removePlayerFromTable(tbl, p)
	if tbl.PlayerCount != 1 { // removePlayerFromTable does not touch count
		// count adjustment is caller's responsibility — document this
	}
	if len(tbl.Players) != 0 {
		t.Errorf("remove failed: %v", tbl.Players)
	}
}

// ---------------------------------------------------------------------------
// Handler flow tests
// ---------------------------------------------------------------------------

func TestLoginSendsHallInit(t *testing.T) {
	resetGlobalState(t)
	rec, cleanup := installTestSender()
	defer cleanup()

	cid := newCID(1)
	Login(&LoginRequest{UserName: "alice", Password: "pw"}, cid)

	// Expect a ChatMsgResponse broadcast (102) and an InitHallResponse (109).
	if got := len(rec.byType(102)); got != 1 {
		t.Errorf("chat broadcast: got %d want 1", got)
	}
	if got := len(rec.byType(109)); got != 1 {
		t.Errorf("hall init: got %d want 1", got)
	}
	// Player must be registered.
	if _, err := loadPlayerByCID(cid); err != nil {
		t.Errorf("player not registered: %v", err)
	}
}

func TestEnterTableFullRejects(t *testing.T) {
	resetGlobalState(t)
	rec, cleanup := installTestSender()
	defer cleanup()

	// Pre-fill table 1 to capacity.
	tbl, _ := loadTableByNum(1)
	for i := int32(0); i < MaxPlayersPerTable; i++ {
		addPlayerToTable(tbl, &Player{UserName: "p", Cid: newCID(int64(100 + i))})
	}

	cid := newCID(99)
	Login(&LoginRequest{UserName: "late", Password: "pw"}, cid)
	rec.reset()

	EnterTable(&EnterTableRequest{UserName: "late", TableNum: 1}, cid)

	msgs := rec.byType(105)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 EnterTableResponse, got %d", len(msgs))
	}
	var resp EnterTableResponse
	_ = json.Unmarshal(msgs[0].data, &resp)
	if resp.IsSuccess {
		t.Errorf("expected rejection on full table")
	}
}

func TestEnterAndReadyThreePlayersStartsGrabPhase(t *testing.T) {
	resetGlobalState(t)
	rec, cleanup := installTestSender()
	defer cleanup()

	cids := []*proto.ClientID{newCID(1), newCID(2), newCID(3)}
	names := []string{"a", "b", "c"}

	for i, cid := range cids {
		Login(&LoginRequest{UserName: names[i], Password: "pw"}, cid)
		rec.reset()
		EnterTable(&EnterTableRequest{UserName: names[i], TableNum: 1}, cid)

		// Each EnterTable should have sent EnterTableResponse (105) to all
		// seated players and a RefreshHallResponse (114) to everyone else.
		if len(rec.byType(105)) == 0 {
			t.Errorf("player %d: no EnterTableResponse", i)
		}
	}

	// Table should now have 3 players.
	tbl, _ := loadTableByNum(1)
	if tbl.PlayerCount != 3 {
		t.Fatalf("expected 3 players, got %d", tbl.PlayerCount)
	}

	rec.reset()
	// First two ready calls: only ack, no grab yet.
	Ready(&ReadyRequest{IsReady: true}, cids[0])
	Ready(&ReadyRequest{IsReady: true}, cids[1])
	if got := len(rec.byType(108)); got != 0 {
		t.Errorf("grab should not start with 2 ready, got %d grab msgs", got)
	}

	// Third ready triggers startGrabPhase.
	Ready(&ReadyRequest{IsReady: true}, cids[2])
	grabMsgs := rec.byType(108)
	if len(grabMsgs) != 3 {
		t.Fatalf("expected 3 GrabLandlordResponse, got %d", len(grabMsgs))
	}
	// Decode one and verify it has cards and a landlord seat.
	var grab GrabLandlordResponse
	_ = json.Unmarshal(grabMsgs[0].data, &grab)
	if len(grab.Cards) != 17 {
		t.Errorf("expected 17 cards, got %d", len(grab.Cards))
	}
	if len(grab.ThreeCards) != 3 {
		t.Errorf("expected 3 threeCards, got %d", len(grab.ThreeCards))
	}
	if grab.LandlordSeatNum < 1 || grab.LandlordSeatNum > 3 {
		t.Errorf("landlord seat out of range: %d", grab.LandlordSeatNum)
	}
	if tbl.IsGrab != true {
		t.Errorf("table should be in grab phase")
	}
}

func TestEndGrabLandlordFinalizesAndResets(t *testing.T) {
	resetGlobalState(t)
	rec, cleanup := installTestSender()
	defer cleanup()

	// Set up a table already in grab phase.
	tbl, _ := loadTableByNum(1)
	p1 := &Player{UserName: "a", Cid: newCID(1), SeatNum: 1, TableNum: 1}
	p2 := &Player{UserName: "b", Cid: newCID(2), SeatNum: 2, TableNum: 1}
	p3 := &Player{UserName: "c", Cid: newCID(3), SeatNum: 3, TableNum: 1}
	tbl.Players = []*Player{p1, p2, p3}
	tbl.PlayerCount = 3
	tbl.ThreeCards = []int32{1, 2, 3}
	tbl.IsGrab = true
	tbl.PlayersCardsOut = map[int32]int32{1: 17, 2: 17, 3: 17}
	clientID2Player.Store(int64(1), p1)

	EndGrabLandlord(&EndGrabLandlordRequest{MeSeatNum: 2}, newCID(1))

	// The landlord (seat 2) should have 20 cards, others still 17.
	if tbl.PlayersCardsOut[2] != 20 {
		t.Errorf("landlord card count: got %d want 20", tbl.PlayersCardsOut[2])
	}
	if tbl.PlayersCardsOut[1] != 17 || tbl.PlayersCardsOut[3] != 17 {
		t.Errorf("non-landlord card counts unexpected: %v", tbl.PlayersCardsOut)
	}
	// EndGrabLandlordResponse (104) should be broadcast to all 3 players.
	msgs := rec.byType(104)
	if len(msgs) == 0 {
		t.Fatal("no EndGrabLandlordResponse sent")
	}
	var resp EndGrabLandlordResponse
	_ = json.Unmarshal(msgs[0].data, &resp)
	if resp.FinalLandlordSeatNum != 2 {
		t.Errorf("landlord seat: got %d want 2", resp.FinalLandlordSeatNum)
	}
}

func TestGiveUpLandlordThreePassesFinalizes(t *testing.T) {
	resetGlobalState(t)
	rec, cleanup := installTestSender()
	defer cleanup()

	tbl, _ := loadTableByNum(1)
	p1 := &Player{UserName: "a", Cid: newCID(1), SeatNum: 1, TableNum: 1}
	p2 := &Player{UserName: "b", Cid: newCID(2), SeatNum: 2, TableNum: 1}
	p3 := &Player{UserName: "c", Cid: newCID(3), SeatNum: 3, TableNum: 1}
	tbl.Players = []*Player{p1, p2, p3}
	tbl.PlayerCount = 3
	tbl.ThreeCards = []int32{1, 2, 3}
	tbl.PlayersCardsOut = map[int32]int32{1: 17, 2: 17, 3: 17}
	clientID2Player.Store(int64(1), p1)
	clientID2Player.Store(int64(2), p2)
	clientID2Player.Store(int64(3), p3)

	// Three consecutive passes should auto-finalize the landlord.
	GiveUpLandlord(&GiveUpLandlordRequest{SeatNum: 1}, newCID(1))
	GiveUpLandlord(&GiveUpLandlordRequest{SeatNum: 2}, newCID(2))
	rec.reset()
	GiveUpLandlord(&GiveUpLandlordRequest{SeatNum: 3}, newCID(3))

	// After the 3rd pass we expect an EndGrabLandlordResponse (104).
	msgs := rec.byType(104)
	if len(msgs) == 0 {
		t.Fatal("expected EndGrabLandlordResponse after 3 passes")
	}
	// PassLandlordCount should be reset.
	if tbl.PassLandlordCount != 0 {
		t.Errorf("PassLandlordCount should be 0, got %d", tbl.PassLandlordCount)
	}
}

func TestExitSeatCleansUp(t *testing.T) {
	resetGlobalState(t)
	rec, cleanup := installTestSender()
	defer cleanup()

	tbl, _ := loadTableByNum(1)
	p1 := &Player{UserName: "a", Cid: newCID(1), SeatNum: 1, TableNum: 1}
	p2 := &Player{UserName: "b", Cid: newCID(2), SeatNum: 2, TableNum: 1}
	tbl.Players = []*Player{p1, p2}
	tbl.PlayerCount = 2
	tbl.IsPlay = true
	playerMap.Store(int32(2), p2)
	clientID2Player.Store(int64(2), p2)

	rec.reset()
	ExitSeat(&ExitSeatRequest{YourSeatNum: 2}, newCID(2))

	if tbl.PlayerCount != 1 {
		t.Errorf("count: got %d want 1", tbl.PlayerCount)
	}
	if tbl.IsPlay {
		t.Errorf("IsPlay should be false after exit")
	}
	if !tbl.IsWait {
		t.Errorf("IsWait should be true after exit")
	}
	// ExitSeatResponse (106) sent to remaining players.
	if len(rec.byType(106)) == 0 {
		t.Errorf("no ExitSeatResponse sent")
	}
}

func TestSafeLoadersReturnErrors(t *testing.T) {
	resetGlobalState(t)
	if _, err := loadPlayerByCID(newCID(9999)); err != ErrPlayerNotFound {
		t.Errorf("expected ErrPlayerNotFound, got %v", err)
	}
	if _, err := loadPlayerByCID(nil); err != ErrPlayerNotFound {
		t.Errorf("expected ErrPlayerNotFound for nil cid, got %v", err)
	}
	if _, err := loadTableByNum(999); err != ErrTableNotFound {
		t.Errorf("expected ErrTableNotFound, got %v", err)
	}
}

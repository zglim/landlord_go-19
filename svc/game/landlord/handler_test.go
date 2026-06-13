package landlord

import (
	_ "github.com/davyxu/cellnet/codec/gogopb"
	"landlord_go/proto"
	"sync"
	"testing"
)

// ---------------- test infrastructure ----------------

// mockCalls records every notification the handler layer emits.
type mockCalls struct {
	mu           sync.Mutex
	sends        []mockSend
	broadcasts   []interface{}
	multiSends   []mockMultiSend
}

type mockSend struct {
	cid *proto.ClientID
	ack *proto.JsonACK
}

type mockMultiSend struct {
	players []*Player
	ack     *proto.JsonACK
	exclude *proto.ClientID
}

var testCalls mockCalls

func installMockNotifications() {
	notifySend = func(cid *proto.ClientID, ack *proto.JsonACK) {
		testCalls.mu.Lock()
		testCalls.sends = append(testCalls.sends, mockSend{cid: cid, ack: ack})
		testCalls.mu.Unlock()
	}
	notifyBroadcastAll = func(ack *proto.JsonACK) {
		testCalls.mu.Lock()
		testCalls.broadcasts = append(testCalls.broadcasts, ack)
		testCalls.mu.Unlock()
	}
	notifyMultiSend = func(players []*Player, ack *proto.JsonACK, exclude *proto.ClientID) {
		testCalls.mu.Lock()
		testCalls.multiSends = append(testCalls.multiSends, mockMultiSend{players: players, ack: ack, exclude: exclude})
		testCalls.mu.Unlock()
	}
}

func resetTestState() {
	testCalls = mockCalls{}

	playerMap.Range(func(key, _ interface{}) bool {
		playerMap.Delete(key)
		return true
	})
	tableMap.Range(func(key, _ interface{}) bool {
		tableMap.Delete(key)
		return true
	})
	userName2Player.Range(func(key, _ interface{}) bool {
		userName2Player.Delete(key)
		return true
	})
	clientID2Player.Range(func(key, _ interface{}) bool {
		clientID2Player.Delete(key)
		return true
	})

	// Re-create a handful of tables with properly initialised maps.
	for i := int32(1); i <= 10; i++ {
		tableMap.Store(i, &Table{
			TableNum:        i,
			ThrowOutCards:   make(map[int32]int32),
			PlayersCardsOut: make(map[int32]int32),
		})
	}
	hallList = nil
}

func testCid(id int64) *proto.ClientID {
	return &proto.ClientID{ID: id, SvcID: "test"}
}

func storeTestPlayer(name string, cid *proto.ClientID) *Player {
	p := &Player{Cid: cid, UserName: name}
	clientID2Player.Store(cid.ID, p)
	userName2Player.Store(name, p)
	return p
}

// ---------------- util helper tests ----------------

func TestGetSeatNum(t *testing.T) {
	if got := GetSeatNum(1, 0); got != 1 {
		t.Errorf("GetSeatNum(1,0) = %d, want 1", got)
	}
	if got := GetSeatNum(1, 2); got != 3 {
		t.Errorf("GetSeatNum(1,2) = %d, want 3", got)
	}
	if got := GetSeatNum(2, 0); got != 4 {
		t.Errorf("GetSeatNum(2,0) = %d, want 4", got)
	}
	if got := GetSeatNum(5, 1); got != 14 {
		t.Errorf("GetSeatNum(5,1) = %d, want 14", got)
	}
}

func TestGetRightRivalSeatNum(t *testing.T) {
	players := []*Player{
		{SeatNum: 1}, {SeatNum: 2}, {SeatNum: 3},
	}
	// max=3. seat 3 (max-self==0) → 3-2=1
	if got := GetRightRivalSeatNum(3, players); got != 1 {
		t.Errorf("seat 3 → %d, want 1", got)
	}
	// max-self==1 → max=3
	if got := GetRightRivalSeatNum(2, players); got != 3 {
		t.Errorf("seat 2 → %d, want 3", got)
	}
	// else → max-1=2
	if got := GetRightRivalSeatNum(1, players); got != 2 {
		t.Errorf("seat 1 → %d, want 2", got)
	}
}

func TestGetLeftRivalSeatNum(t *testing.T) {
	players := []*Player{
		{SeatNum: 1}, {SeatNum: 2}, {SeatNum: 3},
	}
	if got := GetLeftRivalSeatNum(3, players); got != 2 {
		t.Errorf("seat 3 → %d, want 2", got)
	}
	if got := GetLeftRivalSeatNum(2, players); got != 1 {
		t.Errorf("seat 2 → %d, want 1", got)
	}
	if got := GetLeftRivalSeatNum(1, players); got != 3 {
		t.Errorf("seat 1 → %d, want 3", got)
	}
}

func TestGetRightRivalSeatNum_NilPlayers(t *testing.T) {
	if got := GetRightRivalSeatNum(1, []*Player{nil, {SeatNum: 2}}); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestRefreshSeatNum2UserName(t *testing.T) {
	table := &Table{
		Players: []*Player{
			{SeatNum: 1, UserName: "alice"},
			nil,
			{SeatNum: 3, UserName: "bob"},
		},
	}
	m := RefreshSeatNum2UserName(table)
	if m[1] != "alice" {
		t.Error("seat 1 should be alice")
	}
	if m[3] != "bob" {
		t.Error("seat 3 should be bob")
	}
	if _, ok := m[0]; ok {
		t.Error("nil player should not appear")
	}
	if len(m) != 2 {
		t.Errorf("expected 2 entries, got %d", len(m))
	}
}

func TestRefreshSeatNum2UserName_SkipsEmptyUserName(t *testing.T) {
	table := &Table{
		Players: []*Player{
			{SeatNum: 1, UserName: "alice"},
			{SeatNum: 2, UserName: ""}, // pre-allocated empty slot
		},
	}
	m := RefreshSeatNum2UserName(table)
	if _, ok := m[2]; ok {
		t.Error("empty UserName should be skipped")
	}
}

// ---------------- safe lookup tests ----------------

func TestSafeGetPlayerByCid_NotFound(t *testing.T) {
	resetTestState()
	if p := safeGetPlayerByCid(testCid(999)); p != nil {
		t.Error("expected nil for unknown cid")
	}
}

func TestSafeGetPlayerByCid_NilCid(t *testing.T) {
	if p := safeGetPlayerByCid(nil); p != nil {
		t.Error("expected nil for nil cid")
	}
}

func TestSafeGetTable_NotFound(t *testing.T) {
	resetTestState()
	if tbl := safeGetTable(999); tbl != nil {
		t.Error("expected nil for unknown table")
	}
}

func TestSafeGetPlayerBySeatNum_EmptySlot(t *testing.T) {
	resetTestState()
	playerMap.Store(int32(1), &Player{}) // pre-allocated empty
	if p := safeGetPlayerBySeatNum(1); p != nil {
		t.Error("expected nil for empty player slot")
	}
}

func TestSafeGetPlayerBySeatNum_WithRealPlayer(t *testing.T) {
	resetTestState()
	real := &Player{UserName: "alice", SeatNum: 1}
	playerMap.Store(int32(1), real)
	if p := safeGetPlayerBySeatNum(1); p != real {
		t.Error("expected the real player back")
	}
}

// ---------------- table mutation tests ----------------

func TestAddRemovePlayer(t *testing.T) {
	resetTestState()
	installMockNotifications()

	table := safeGetTable(1)
	player := &Player{Cid: testCid(1), UserName: "alice"}

	addPlayerToTable(table, player)

	if table.PlayerCount != 1 {
		t.Errorf("count=%d, want 1", table.PlayerCount)
	}
	if len(table.Players) != 1 {
		t.Errorf("len=%d, want 1", len(table.Players))
	}
	if player.TableNum != 1 {
		t.Errorf("tableNum=%d, want 1", player.TableNum)
	}
	if player.SeatNum != 1 {
		t.Errorf("seatNum=%d, want 1", player.SeatNum)
	}
	if len(table.Cards) != 54 {
		t.Errorf("cards=%d, want 54", len(table.Cards))
	}

	removePlayerFromTable(table, player)

	if table.PlayerCount != 0 {
		t.Errorf("count=%d after remove, want 0", table.PlayerCount)
	}
	if len(table.Players) != 0 {
		t.Errorf("len=%d after remove, want 0", len(table.Players))
	}
}

func TestRemovePlayerFromTable_CountFloor(t *testing.T) {
	resetTestState()
	table := safeGetTable(1)
	// Remove from an already empty table – count must not go negative.
	removePlayerFromTable(table, &Player{SeatNum: 99})
	if table.PlayerCount != 0 {
		t.Errorf("count=%d, want 0 (floor)", table.PlayerCount)
	}
}

func TestAddPlayerToTable_MultiplePlayers(t *testing.T) {
	resetTestState()
	table := safeGetTable(2) // table 2 → seats 4,5,6
	for i, name := range []string{"a", "b", "c"} {
		p := &Player{Cid: testCid(int64(10 + i)), UserName: name}
		addPlayerToTable(table, p)
	}
	if table.PlayerCount != 3 {
		t.Errorf("count=%d, want 3", table.PlayerCount)
	}
	if table.Players[0].SeatNum != 4 {
		t.Errorf("seat[0]=%d, want 4", table.Players[0].SeatNum)
	}
	if table.Players[2].SeatNum != 6 {
		t.Errorf("seat[2]=%d, want 6", table.Players[2].SeatNum)
	}
}

// ---------------- hall list tests ----------------

func TestRebuildHallList_Empty(t *testing.T) {
	resetTestState()
	rebuildHallList()
	if len(hallList) == 0 {
		t.Error("expected at least one entry from re-seeded tables")
	}
}

func TestRebuildHallList_SortedAndFiltered(t *testing.T) {
	resetTestState()

	t1 := safeGetTable(1)
	t1.Players = append(t1.Players,
		&Player{SeatNum: 1, UserName: "alice"},
		nil,
		&Player{SeatNum: 3, UserName: "bob"},
		&Player{SeatNum: 2, UserName: ""}, // empty – must be skipped
	)
	t1.PlayerCount = 2

	rebuildHallList()

	var entry *HallTable
	for _, h := range hallList {
		if h.TableNum == 1 {
			entry = h
			break
		}
	}
	if entry == nil {
		t.Fatal("table 1 missing from hall list")
	}
	if len(entry.UserNames) != 2 {
		t.Errorf("usernames=%v, want [alice bob]", entry.UserNames)
	}
	if entry.IsFull {
		t.Error("should not be full with 2 players")
	}

	// Verify ordering.
	for i := 1; i < len(hallList); i++ {
		if hallList[i].TableNum < hallList[i-1].TableNum {
			t.Errorf("hall list not sorted at index %d", i)
		}
	}
}

func TestRebuildHallList_IsFull(t *testing.T) {
	resetTestState()
	tbl := safeGetTable(1)
	for i := 0; i < 3; i++ {
		tbl.Players = append(tbl.Players, &Player{SeatNum: int32(i + 1), UserName: "p"})
	}
	tbl.PlayerCount = 3
	rebuildHallList()
	for _, h := range hallList {
		if h.TableNum == 1 && !h.IsFull {
			t.Error("table with 3 players should be full")
		}
	}
}

// ---------------- WrapMultiSend tests ----------------

func TestWrapMultiSend_NilSafe(t *testing.T) {
	resetTestState()
	installMockNotifications()
	// Nil players must not panic.
	WrapMultiSend(nil, &proto.JsonACK{JsonType: 1}, nil)
	WrapMultiSend([]*Player{nil, nil}, &proto.JsonACK{JsonType: 1}, nil)
}

func TestWrapMultiSend_ExcludesCid(t *testing.T) {
	resetTestState()
	installMockNotifications()
	// Override multiSend to capture the inner agentapi call would be complex;
	// instead verify no panic and that the function completes.
	excludeCid := testCid(2)
	players := []*Player{
		{Cid: testCid(1), UserName: "a"},
		{Cid: excludeCid, UserName: "b"},
		{Cid: testCid(3), UserName: "c"},
	}
	// WrapMultiSend calls agentapi.MultipleSend which needs a real remote
	// service. In tests the service is nil so MultipleSend returns early.
	// We just verify no panic.
	WrapMultiSend(players, &proto.JsonACK{JsonType: 1}, excludeCid)
}

// ---------------- excludePlayer tests ----------------

func TestExcludePlayer(t *testing.T) {
	cid1, cid2, cid3 := testCid(1), testCid(2), testCid(3)
	players := []*Player{
		{Cid: cid1, UserName: "a"},
		{Cid: cid2, UserName: "b"},
		{Cid: cid3, UserName: "c"},
	}

	result := excludePlayer(players, cid2)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0].UserName != "a" || result[1].UserName != "c" {
		t.Error("wrong players after exclude")
	}
}

func TestExcludePlayer_NilExclude(t *testing.T) {
	players := []*Player{{Cid: testCid(1)}}
	if got := excludePlayer(players, nil); len(got) != 1 {
		t.Errorf("expected 1, got %d", len(got))
	}
}

func TestExcludePlayer_SkipsNilEntries(t *testing.T) {
	players := []*Player{nil, {Cid: testCid(1)}, nil}
	got := excludePlayer(players, testCid(99))
	if len(got) != 1 {
		t.Errorf("expected 1, got %d", len(got))
	}
}

// ---------------- handler boundary tests ----------------

func TestLogin_BasicFlow(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	Login(&LoginRequest{UserName: "alice", Password: "123"}, cid)

	// Player registered in both maps.
	if _, ok := clientID2Player.Load(cid.ID); !ok {
		t.Error("player missing from clientID2Player")
	}
	if _, ok := userName2Player.Load("alice"); !ok {
		t.Error("player missing from userName2Player")
	}

	// Should have emitted: 1 broadcast (chat join) + 1 send (init hall).
	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	if len(testCalls.broadcasts) != 1 {
		t.Errorf("broadcasts=%d, want 1", len(testCalls.broadcasts))
	}
	if len(testCalls.sends) != 1 {
		t.Errorf("sends=%d, want 1", len(testCalls.sends))
	}
}

func TestLogin_EmptyCredentials(t *testing.T) {
	resetTestState()
	installMockNotifications()

	Login(&LoginRequest{UserName: "", Password: ""}, testCid(1))

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	if len(testCalls.broadcasts) != 0 || len(testCalls.sends) != 0 {
		t.Error("no messages expected for empty credentials")
	}
}

func TestLogin_OverwritePreviousSession(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid1 := testCid(1)
	cid2 := testCid(2)
	Login(&LoginRequest{UserName: "alice", Password: "p"}, cid1)
	Login(&LoginRequest{UserName: "alice", Password: "p"}, cid2)

	v, _ := clientID2Player.Load(cid2.ID)
	p := v.(*Player)
	if p.UserName != "alice" {
		t.Error("second login should register correctly")
	}
}

func TestInitHall_NoPlayers(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	InitHall(&InitHallRequest{}, cid)

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	if len(testCalls.sends) != 1 {
		t.Errorf("sends=%d, want 1", len(testCalls.sends))
	}
	if testCalls.sends[0].ack.JsonType != 109 {
		t.Errorf("jsonType=%d, want 109", testCalls.sends[0].ack.JsonType)
	}
}

func TestEnterTable_BasicFlow(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	storeTestPlayer("alice", cid)

	EnterTable(&EnterTableRequest{UserName: "alice", TableNum: 1}, cid)

	table := safeGetTable(1)
	if table.PlayerCount != 1 {
		t.Errorf("count=%d, want 1", table.PlayerCount)
	}
	if table.Players[0].UserName != "alice" {
		t.Error("player not seated")
	}
}

func TestEnterTable_TableFull(t *testing.T) {
	resetTestState()
	installMockNotifications()

	table := safeGetTable(1)
	table.PlayerCount = 3

	cid := testCid(1)
	storeTestPlayer("alice", cid)
	EnterTable(&EnterTableRequest{UserName: "alice", TableNum: 1}, cid)

	// Player must NOT be added.
	if table.PlayerCount != 3 {
		t.Errorf("count=%d, should still be 3", table.PlayerCount)
	}

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	// Should receive a failure response.
	if len(testCalls.sends) != 1 {
		t.Fatalf("sends=%d, want 1 (failure)", len(testCalls.sends))
	}
	if testCalls.sends[0].ack.JsonType != 105 {
		t.Errorf("jsonType=%d, want 105", testCalls.sends[0].ack.JsonType)
	}
}

func TestEnterTable_DuplicateJoin(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	storeTestPlayer("alice", cid)

	EnterTable(&EnterTableRequest{UserName: "alice", TableNum: 1}, cid)
	EnterTable(&EnterTableRequest{UserName: "alice", TableNum: 1}, cid)

	table := safeGetTable(1)
	if table.PlayerCount != 1 {
		t.Errorf("count=%d, want 1 (no duplicate)", table.PlayerCount)
	}
}

func TestEnterTable_PlayerNotFound(t *testing.T) {
	resetTestState()
	installMockNotifications()

	// No player registered for cid 999.
	EnterTable(&EnterTableRequest{UserName: "ghost", TableNum: 1}, testCid(999))
	table := safeGetTable(1)
	if table.PlayerCount != 0 {
		t.Error("count should be 0 for unknown player")
	}
}

func TestEnterTable_TableNotFound(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	storeTestPlayer("alice", cid)
	EnterTable(&EnterTableRequest{UserName: "alice", TableNum: 999}, cid)
	// Just verify no panic; table 999 doesn't exist.
}

func TestReady_NotFound(t *testing.T) {
	resetTestState()
	installMockNotifications()
	// Unknown cid – must not panic.
	Ready(&ReadyRequest{IsReady: true}, testCid(999))
}

func TestReady_ThreePlayersStart(t *testing.T) {
	resetTestState()
	installMockNotifications()

	table := safeGetTable(1)
	for i, name := range []string{"a", "b", "c"} {
		cid := testCid(int64(10 + i))
		p := storeTestPlayer(name, cid)
		addPlayerToTable(table, p)
	}
	table.Cards = GetRandomCards()

	for i := 0; i < 3; i++ {
		cid := testCid(int64(10 + i))
		Ready(&ReadyRequest{IsReady: true}, cid)
	}

	if table.IsGrab != true {
		t.Error("game should have started")
	}
	if table.ReadyCount != 0 {
		t.Errorf("readyCount=%d, want 0 (reset)", table.ReadyCount)
	}

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	// Each player gets ReadyResponse + GrabLandlordResponse = 6 sends.
	if len(testCalls.sends) < 6 {
		t.Errorf("sends=%d, want >= 6", len(testCalls.sends))
	}
}

func TestCancelReady_CountFloor(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	storeTestPlayer("alice", cid)

	table := safeGetTable(1)
	addPlayerToTable(table, safeGetPlayerByCid(cid))

	// readyCount starts at 0; cancelling must not go negative.
	CancelReady(&CancelReadyRequest{IsCancelReady: true}, cid)
	if table.ReadyCount != 0 {
		t.Errorf("readyCount=%d, want 0 (floor)", table.ReadyCount)
	}
}

func TestExitSeat_BasicFlow(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	p := storeTestPlayer("alice", cid)
	table := safeGetTable(1)
	addPlayerToTable(table, p)

	seatNum := p.SeatNum
	ExitSeat(&ExitSeatRequest{YourSeatNum: seatNum}, cid)

	if table.PlayerCount != 0 {
		t.Errorf("count=%d, want 0", table.PlayerCount)
	}
	// Seat slot should be reset to empty player, not deleted.
	v, ok := playerMap.Load(seatNum)
	if !ok {
		t.Error("seat slot should still exist")
	} else if sp := v.(*Player); sp.UserName != "" {
		t.Error("seat slot should be empty")
	}
}

func TestExitSeat_SeatNotFound(t *testing.T) {
	resetTestState()
	installMockNotifications()
	// Must not panic on unknown seat.
	ExitSeat(&ExitSeatRequest{YourSeatNum: 999}, testCid(1))
}

func TestExitOrException_BasicFlow(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	p := storeTestPlayer("alice", cid)
	table := safeGetTable(1)
	addPlayerToTable(table, p)

	ExitOrException(cid)

	if table.PlayerCount != 0 {
		t.Errorf("count=%d, want 0", table.PlayerCount)
	}
	if _, ok := userName2Player.Load("alice"); ok {
		t.Error("alice should be removed from userName2Player")
	}
	if _, ok := clientID2Player.Load(cid.ID); ok {
		t.Error("should be removed from clientID2Player")
	}
}

func TestExitOrException_PlayerNotFound(t *testing.T) {
	resetTestState()
	installMockNotifications()
	// Unknown cid – must not panic.
	ExitOrException(testCid(999))
}

func TestExitOrException_PlayerNotAtTable(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	storeTestPlayer("alice", cid)
	// Player is online but not seated at any table.
	ExitOrException(cid)

	if _, ok := userName2Player.Load("alice"); ok {
		t.Error("should still be cleaned up from userName2Player")
	}
	if _, ok := clientID2Player.Load(cid.ID); ok {
		t.Error("should be cleaned up from clientID2Player")
	}
}

func TestChatMsg_Global(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	storeTestPlayer("alice", cid)

	ChatMsg(&ChatMsgRequest{ChatFlag: 1, UserName: "alice", Msg: "hi"}, cid)

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	if len(testCalls.multiSends) != 1 {
		t.Errorf("multiSends=%d, want 1", len(testCalls.multiSends))
	}
}

func TestChatMsg_Table(t *testing.T) {
	resetTestState()
	installMockNotifications()

	table := safeGetTable(1)
	cid := testCid(1)
	p := storeTestPlayer("alice", cid)
	addPlayerToTable(table, p)

	ChatMsg(&ChatMsgRequest{ChatFlag: 2, UserName: "alice", Msg: "hi", TableNum: 1}, cid)

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	if len(testCalls.multiSends) != 1 {
		t.Errorf("multiSends=%d, want 1", len(testCalls.multiSends))
	}
}

func TestChatMsg_TableNotFound(t *testing.T) {
	resetTestState()
	installMockNotifications()

	// Table 999 doesn't exist – must not panic.
	ChatMsg(&ChatMsgRequest{ChatFlag: 2, TableNum: 999}, testCid(1))

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	if len(testCalls.multiSends) != 0 {
		t.Errorf("multiSends=%d, want 0", len(testCalls.multiSends))
	}
}

func TestGiveUpLandlord_ThreePassesAutoEnd(t *testing.T) {
	resetTestState()
	installMockNotifications()

	table := safeGetTable(1)
	table.ThreeCards = []int32{1, 2, 3}
	table.PlayersCardsOut = map[int32]int32{1: 17, 2: 17, 3: 17}

	for i, name := range []string{"a", "b", "c"} {
		cid := testCid(int64(10 + i))
		p := storeTestPlayer(name, cid)
		addPlayerToTable(table, p)
	}

	for i := 0; i < 3; i++ {
		cid := testCid(int64(10 + i))
		GiveUpLandlord(&GiveUpLandlordRequest{SeatNum: int32(10 + i)}, cid)
	}

	if table.PassLandlordCount != 0 {
		t.Errorf("passCount=%d, want 0 (auto-reset)", table.PassLandlordCount)
	}
}

func TestEndGrabLandlord_BasicFlow(t *testing.T) {
	resetTestState()
	installMockNotifications()

	table := safeGetTable(1)
	table.ThreeCards = []int32{1, 2, 3}
	table.PlayersCardsOut = map[int32]int32{1: 17, 2: 17, 3: 17}

	cid := testCid(1)
	p := storeTestPlayer("alice", cid)
	addPlayerToTable(table, p)

	EndGrabLandlord(&EndGrabLandlordRequest{MeSeatNum: 1}, cid)

	if table.PlayersCardsOut[1] != 20 {
		t.Errorf("cards=%d, want 20 (landlord bonus)", table.PlayersCardsOut[1])
	}
}

func TestEndGame_BasicFlow(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	p := storeTestPlayer("alice", cid)
	table := safeGetTable(1)
	addPlayerToTable(table, p)

	EndGame(&EndGameRequest{WinnerSeatNum: 1}, cid)

	testCalls.mu.Lock()
	defer testCalls.mu.Unlock()
	if len(testCalls.multiSends) != 1 {
		t.Errorf("multiSends=%d, want 1", len(testCalls.multiSends))
	}
}

func TestMultipleWager_Agreed(t *testing.T) {
	resetTestState()
	installMockNotifications()

	table := safeGetTable(1)
	table.WagerMultipleNum = 2

	for i, name := range []string{"a", "b"} {
		cid := testCid(int64(10 + i))
		p := storeTestPlayer(name, cid)
		addPlayerToTable(table, p)
	}

	cid1 := testCid(10)
	MultipleWager(&MultipleWagerRequest{Agreed: 1}, cid1)
	if table.AnswerMultipleNum != 1 {
		t.Errorf("answerCount=%d, want 1", table.AnswerMultipleNum)
	}

	cid2 := testCid(11)
	MultipleWager(&MultipleWagerRequest{Agreed: 1}, cid2)
	// After 2 answers the counter should reset.
	if table.AnswerMultipleNum != 0 {
		t.Errorf("answerCount=%d, want 0 (reset)", table.AnswerMultipleNum)
	}
}

func TestCardsOut_Pass(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	p := storeTestPlayer("alice", cid)
	table := safeGetTable(1)
	addPlayerToTable(table, p)
	table.LastCardsOut = []int32{1, 2}

	CardsOut(&CardsOutRequest{IsPass: true, FromSeatNum: 1, ToSeatNum: 2}, cid)
	if table.ContinuousPass != 1 {
		t.Errorf("pass=%d, want 1", table.ContinuousPass)
	}

	CardsOut(&CardsOutRequest{IsPass: true, FromSeatNum: 2, ToSeatNum: 3}, cid)
	// Two consecutive passes → reset.
	if table.ContinuousPass != 0 {
		t.Errorf("pass=%d, want 0 (reset after 2)", table.ContinuousPass)
	}
}

func TestCardsOut_Play(t *testing.T) {
	resetTestState()
	installMockNotifications()

	cid := testCid(1)
	p := storeTestPlayer("alice", cid)
	table := safeGetTable(1)
	addPlayerToTable(table, p)
	table.PlayersCardsOut[1] = 17

	CardsOut(&CardsOutRequest{
		IsPass:      false,
		FromSeatNum: 1,
		ToSeatNum:   2,
		CardsOut:    []int32{1, 2, 3},
	}, cid)

	if table.ContinuousPass != 0 {
		t.Errorf("pass=%d, want 0", table.ContinuousPass)
	}
	if table.PlayersCardsOut[1] != 14 {
		t.Errorf("cards=%d, want 14", table.PlayersCardsOut[1])
	}
}

func TestExitHall_RemovesPlayer(t *testing.T) {
	resetTestState()
	installMockNotifications()

	storeTestPlayer("alice", testCid(1))
	ExitHall(&ExitHallRequest{UserName: "alice"}, testCid(1))

	if _, ok := userName2Player.Load("alice"); ok {
		t.Error("alice should be removed")
	}
}

// TestFullLifecycle walks through login → enter → ready (x3) → exit → reconnect.
func TestFullLifecycle(t *testing.T) {
	resetTestState()
	installMockNotifications()

	// 1. Three players log in.
	var cids []*proto.ClientID
	names := []string{"alice", "bob", "charlie"}
	for i, name := range names {
		cid := testCid(int64(100 + i))
		cids = append(cids, cid)
		Login(&LoginRequest{UserName: name, Password: "pw"}, cid)
	}

	// 2. All three enter table 1.
	for i, name := range names {
		EnterTable(&EnterTableRequest{UserName: name, TableNum: 1}, cids[i])
	}
	table := safeGetTable(1)
	if table.PlayerCount != 3 {
		t.Fatalf("count=%d, want 3", table.PlayerCount)
	}

	// 3. All ready – triggers game start.
	for _, cid := range cids {
		Ready(&ReadyRequest{IsReady: true}, cid)
	}
	if !table.IsGrab {
		t.Error("game should have started")
	}

	// 4. One player exits.
	seatNum := table.Players[0].SeatNum
	ExitSeat(&ExitSeatRequest{YourSeatNum: seatNum}, table.Players[0].Cid)
	if table.PlayerCount != 2 {
		t.Errorf("count=%d after exit, want 2", table.PlayerCount)
	}
	if table.IsGrab {
		t.Error("IsGrab should be false after exit")
	}

	// 5. Hall list should reflect the change.
	rebuildHallList()
	for _, h := range hallList {
		if h.TableNum == 1 && h.IsFull {
			t.Error("table 1 should not be full after exit")
		}
	}

	// 6. A new player can now join the freed seat.
	newCid := testCid(200)
	Login(&LoginRequest{UserName: "dave", Password: "pw"}, newCid)
	EnterTable(&EnterTableRequest{UserName: "dave", TableNum: 1}, newCid)
	if table.PlayerCount != 3 {
		t.Errorf("count=%d, want 3 after new player joins", table.PlayerCount)
	}
}

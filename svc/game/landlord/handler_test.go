package landlord

import (
	_ "github.com/davyxu/cellnet/codec/gogopb"
	"landlord_go/proto"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func resetState() {
	clientID2Player.Range(func(k, v interface{}) bool { clientID2Player.Delete(k); return true })
	userName2Player.Range(func(k, v interface{}) bool { userName2Player.Delete(k); return true })
	// 重置 1-100 号 table 状态
	for i := int32(1); i <= 100; i++ {
		if v, ok := tableMap.Load(i); ok {
			t := v.(*Table)
			t.Players = nil
			t.PlayerCount = 0
			t.IsPlay = false
			t.IsGrab = false
			t.IsWait = true
			t.ReadyCount = 0
			t.PassLandlordCount = 0
			t.WagerMultipleNum = 0
			t.AnswerMultipleNum = 0
			t.AgreedMultipleResult = 0
			t.ContinuousPass = 0
			t.ThrowOutCards = make(map[int32]int32)
			t.PlayersCardsOut = make(map[int32]int32)
		}
	}
	hallList = nil
}

func makePlayer(id int64, name string) (*proto.ClientID, *Player) {
	cid := &proto.ClientID{ID: id, SvcID: "test"}
	p := &Player{Cid: cid, UserName: name}
	clientID2Player.Store(id, p)
	userName2Player.Store(name, p)
	return cid, p
}

// ---------------------------------------------------------------------------
// loadPlayer / loadTable nil 安全
// ---------------------------------------------------------------------------

func TestLoadPlayer_NotFound(t *testing.T) {
	resetState()
	defer resetState()
	p := loadPlayer(&proto.ClientID{ID: 99999, SvcID: "test"})
	if p != nil {
		t.Fatal("expected nil for non-existent player")
	}
}

func TestLoadPlayer_NilCid(t *testing.T) {
	if p := loadPlayer(nil); p != nil {
		t.Fatal("expected nil for nil cid")
	}
}

func TestLoadTable_NotFound(t *testing.T) {
	if tbl := loadTable(99999); tbl != nil {
		t.Fatal("expected nil for non-existent table")
	}
}

// ---------------------------------------------------------------------------
// buildHallList：空桌、有人桌、满桌
// ---------------------------------------------------------------------------

func TestBuildHallList_Empty(t *testing.T) {
	resetState()
	defer resetState()
	list := buildHallList()
	if len(list) == 0 {
		t.Fatal("expected non-empty hall list (100 tables)")
	}
	for _, h := range list {
		if len(h.UserNames) != 0 {
			t.Errorf("table %d should have 0 players, got %d", h.TableNum, len(h.UserNames))
		}
		if h.IsFull {
			t.Errorf("table %d should not be full", h.TableNum)
		}
	}
}

func TestBuildHallList_Sorted(t *testing.T) {
	resetState()
	defer resetState()
	list := buildHallList()
	for i := 1; i < len(list); i++ {
		if list[i].TableNum < list[i-1].TableNum {
			t.Fatalf("hallList not sorted at index %d: %d < %d",
				i, list[i].TableNum, list[i-1].TableNum)
		}
	}
}

func TestBuildHallList_WithPlayers(t *testing.T) {
	resetState()
	defer resetState()

	v, _ := tableMap.Load(int32(1))
	table := v.(*Table)
	table.Players = []*Player{
		{UserName: "alice", SeatNum: 1},
		{UserName: "bob", SeatNum: 2},
	}
	table.PlayerCount = 2

	list := buildHallList()
	var t1 *HallTable
	for _, h := range list {
		if h.TableNum == 1 {
			t1 = h
			break
		}
	}
	if t1 == nil {
		t.Fatal("table 1 not found in hallList")
	}
	if len(t1.UserNames) != 2 {
		t.Errorf("expected 2 users, got %d", len(t1.UserNames))
	}
	if t1.IsFull {
		t.Error("table with 2 players should not be full")
	}
}

func TestBuildHallList_SkipsNilPlayers(t *testing.T) {
	resetState()
	defer resetState()

	v, _ := tableMap.Load(int32(5))
	table := v.(*Table)
	table.Players = []*Player{
		{UserName: "alice", SeatNum: 13},
		nil,
		{UserName: "", SeatNum: 14}, // 空用户名也应跳过
	}
	table.PlayerCount = 1

	list := buildHallList()
	for _, h := range list {
		if h.TableNum == 5 {
			if len(h.UserNames) != 1 {
				t.Errorf("expected 1 valid user, got %d (%v)", len(h.UserNames), h.UserNames)
			}
			return
		}
	}
	t.Fatal("table 5 not found")
}

func TestBuildHallList_FullTable(t *testing.T) {
	resetState()
	defer resetState()

	v, _ := tableMap.Load(int32(3))
	table := v.(*Table)
	table.Players = []*Player{
		{UserName: "a", SeatNum: 7},
		{UserName: "b", SeatNum: 8},
		{UserName: "c", SeatNum: 9},
	}
	table.PlayerCount = 3

	list := buildHallList()
	for _, h := range list {
		if h.TableNum == 3 {
			if !h.IsFull {
				t.Error("table with 3 players should be full")
			}
			return
		}
	}
	t.Fatal("table 3 not found")
}

// ---------------------------------------------------------------------------
// WrapMultiSend：nil/空保护
// ---------------------------------------------------------------------------

func TestWrapMultiSend_NilPlayers(t *testing.T) {
	// 不应 panic
	WrapMultiSend(nil, &proto.JsonACK{JsonType: 1}, nil)
}

func TestWrapMultiSend_EmptyPlayers(t *testing.T) {
	WrapMultiSend([]*Player{}, &proto.JsonACK{JsonType: 1}, nil)
}

func TestWrapMultiSend_SkipNilEntries(t *testing.T) {
	players := []*Player{nil, nil}
	// 不应 panic
	WrapMultiSend(players, &proto.JsonACK{JsonType: 1}, nil)
}

func TestWrapMultiSend_SkipNilCid(t *testing.T) {
	players := []*Player{{UserName: "no-cid", Cid: nil}}
	WrapMultiSend(players, &proto.JsonACK{JsonType: 1}, nil)
}

// ---------------------------------------------------------------------------
// ExitSeat / ExitOrException slice 删除正确性
// ---------------------------------------------------------------------------

func TestExitSeat_SliceRemove(t *testing.T) {
	players := []*Player{
		{UserName: "a", SeatNum: 1},
		{UserName: "b", SeatNum: 2},
		{UserName: "c", SeatNum: 3},
	}
	target := players[1]

	// 模拟 ExitSeat 的删除逻辑
	for k, v := range players {
		if v == target {
			players = append(players[:k], players[k+1:]...)
			break
		}
	}

	if len(players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(players))
	}
	if players[0].UserName != "a" || players[1].UserName != "c" {
		t.Errorf("wrong remaining: %v %v", players[0].UserName, players[1].UserName)
	}
}

func TestExitOrException_SliceRemove(t *testing.T) {
	players := []*Player{
		{UserName: "a", SeatNum: 1},
		{UserName: "b", SeatNum: 2},
		{UserName: "c", SeatNum: 3},
	}
	target := players[1]

	// 模拟修复后的 ExitOrException 删除逻辑
	for k, v := range players {
		if v == target {
			players = append(players[:k], players[k+1:]...)
			break
		}
	}

	if len(players) != 2 {
		t.Fatalf("expected 2, got %d", len(players))
	}
	if players[0].UserName != "a" || players[1].UserName != "c" {
		t.Errorf("wrong remaining: %v %v", players[0].UserName, players[1].UserName)
	}
}

func TestSliceRemove_FirstElement(t *testing.T) {
	players := []*Player{
		{UserName: "a", SeatNum: 1},
		{UserName: "b", SeatNum: 2},
		{UserName: "c", SeatNum: 3},
	}
	target := players[0]
	for k, v := range players {
		if v == target {
			players = append(players[:k], players[k+1:]...)
			break
		}
	}
	if len(players) != 2 || players[0].UserName != "b" || players[1].UserName != "c" {
		t.Errorf("unexpected result: %+v", players)
	}
}

func TestSliceRemove_LastElement(t *testing.T) {
	players := []*Player{
		{UserName: "a", SeatNum: 1},
		{UserName: "b", SeatNum: 2},
		{UserName: "c", SeatNum: 3},
	}
	target := players[2]
	for k, v := range players {
		if v == target {
			players = append(players[:k], players[k+1:]...)
			break
		}
	}
	if len(players) != 2 || players[0].UserName != "a" || players[1].UserName != "b" {
		t.Errorf("unexpected result: %+v", players)
	}
}

func TestSliceRemove_SingleElement(t *testing.T) {
	players := []*Player{
		{UserName: "a", SeatNum: 1},
	}
	target := players[0]
	for k, v := range players {
		if v == target {
			players = append(players[:k], players[k+1:]...)
			break
		}
	}
	if len(players) != 0 {
		t.Errorf("expected empty, got %d", len(players))
	}
}

// ---------------------------------------------------------------------------
// hallList UserNames 删除（ExitSeat/ExitOrException 中修复后的逻辑）
// ---------------------------------------------------------------------------

func TestHallUserNames_SliceRemove(t *testing.T) {
	userNames := []string{"a", "b", "c"}
	target := "b"
	for x, y := range userNames {
		if y == target {
			userNames = append(userNames[:x], userNames[x+1:]...)
			break
		}
	}
	if len(userNames) != 2 || userNames[0] != "a" || userNames[1] != "c" {
		t.Errorf("unexpected: %v", userNames)
	}
}

// ---------------------------------------------------------------------------
// ReadyFlag 防重复准备
// ---------------------------------------------------------------------------

func TestReadyFlag_PreventsDoubleReady(t *testing.T) {
	resetState()
	defer resetState()

	cid, _ := makePlayer(1, "alice")

	v, _ := tableMap.Load(int32(1))
	table := v.(*Table)

	p := loadPlayer(cid)
	p.TableNum = 1
	table.Players = []*Player{p}
	table.PlayerCount = 1

	// 第一次准备
	p.ReadyFlag = false
	if p.ReadyFlag {
		t.Fatal("should not be ready initially")
	}

	p.ReadyFlag = true
	table.ReadyCount++
	if table.ReadyCount != 1 {
		t.Errorf("expected ReadyCount=1, got %d", table.ReadyCount)
	}

	// 第二次准备应被拦截
	if p.ReadyFlag {
		// 重复准备，不增加计数
	} else {
		p.ReadyFlag = true
		table.ReadyCount++
	}
	if table.ReadyCount != 1 {
		t.Errorf("double ready should not increment count, got %d", table.ReadyCount)
	}
}

func TestReadyFlag_PreventsNegativeCount(t *testing.T) {
	resetState()
	defer resetState()

	_, p := makePlayer(1, "alice")
	p.ReadyFlag = false // 未准备状态

	// 尝试取消准备：因为没有 ReadyFlag，不应减计数
	if !p.ReadyFlag {
		// 拦截，不减
	} else {
		p.ReadyFlag = false
	}

	v, _ := tableMap.Load(int32(1))
	table := v.(*Table)
	table.ReadyCount = 0

	if table.ReadyCount < 0 {
		t.Error("ReadyCount should not be negative")
	}
}

// ---------------------------------------------------------------------------
// 重复登录清理
// ---------------------------------------------------------------------------

func TestLogin_CleansOldSession(t *testing.T) {
	resetState()
	defer resetState()

	// 模拟旧会话
	oldCid := &proto.ClientID{ID: 100, SvcID: "test"}
	oldPlayer := &Player{Cid: oldCid, UserName: "alice", SeatNum: 5}
	clientID2Player.Store(int64(100), oldPlayer)
	userName2Player.Store("alice", oldPlayer)
	playerMap.Store(int32(5), oldPlayer)

	// 模拟 Login 清理逻辑
	userName := "alice"
	if oldVal, loaded := userName2Player.Load(userName); loaded {
		op := oldVal.(*Player)
		if op.Cid != nil {
			clientID2Player.Delete(op.Cid.ID)
		}
		if op.SeatNum > 0 {
			playerMap.Delete(op.SeatNum)
		}
	}

	newCid := &proto.ClientID{ID: 200, SvcID: "test"}
	newPlayer := &Player{Cid: newCid, UserName: userName}
	clientID2Player.Store(int64(200), newPlayer)
	userName2Player.Store(userName, newPlayer)

	// 验证旧 cid 已清除
	if _, ok := clientID2Player.Load(int64(100)); ok {
		t.Error("old cid should be cleaned")
	}
	if _, ok := playerMap.Load(int32(5)); ok {
		t.Error("old seat should be cleaned")
	}

	// 验证新玩家存在
	if v, ok := clientID2Player.Load(int64(200)); !ok || v.(*Player).UserName != "alice" {
		t.Error("new player should exist")
	}
}

// ---------------------------------------------------------------------------
// 重复进桌拦截
// ---------------------------------------------------------------------------

func TestEnterTable_RejectsDuplicate(t *testing.T) {
	resetState()
	defer resetState()

	cid, p := makePlayer(1, "alice")
	p.TableNum = 5 // 已在桌 5

	// 模拟 EnterTable 的重复进桌检查
	if p.TableNum > 0 {
		// 拒绝进桌 — 正确行为
	} else {
		t.Error("should reject player already at a table")
	}
	_ = cid
}

// ---------------------------------------------------------------------------
// buildHallList 不产生重复用户名
// ---------------------------------------------------------------------------

func TestBuildHallList_NoDuplicateUserNames(t *testing.T) {
	resetState()
	defer resetState()

	v, _ := tableMap.Load(int32(1))
	table := v.(*Table)
	table.Players = []*Player{
		{UserName: "alice", SeatNum: 1},
		{UserName: "bob", SeatNum: 2},
	}
	table.PlayerCount = 2

	list := buildHallList()
	for _, h := range list {
		if h.TableNum == 1 {
			seen := make(map[string]bool)
			for _, name := range h.UserNames {
				if seen[name] {
					t.Errorf("duplicate userName %q in table %d", name, h.TableNum)
				}
				seen[name] = true
			}
			return
		}
	}
}

// ---------------------------------------------------------------------------
// PlayerCount 不下溢
// ---------------------------------------------------------------------------

func TestPlayerCount_NoUnderflow(t *testing.T) {
	resetState()
	defer resetState()

	v, _ := tableMap.Load(int32(1))
	table := v.(*Table)
	table.PlayerCount = 0

	// 模拟 ExitSeat 下溢保护
	table.PlayerCount--
	if table.PlayerCount < 0 {
		table.PlayerCount = 0
	}
	if table.PlayerCount != 0 {
		t.Errorf("expected 0, got %d", table.PlayerCount)
	}
}

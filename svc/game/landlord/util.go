package landlord

import (
	"encoding/json"
	"landlord_go/proto"
	"landlord_go/svc/agent/api"
	"math"
	"math/rand"
	"sort"
	"time"
)

// --------------- Safe lookup helpers ---------------

// safeGetPlayerByCid returns the player for a given client ID.
// Returns nil when the mapping is missing or the stored value is not a *Player.
func safeGetPlayerByCid(cid *proto.ClientID) *Player {
	if cid == nil {
		return nil
	}
	v, ok := clientID2Player.Load(cid.ID)
	if !ok {
		return nil
	}
	p, ok := v.(*Player)
	if !ok {
		return nil
	}
	return p
}

// safeGetPlayerBySeatNum returns the player stored under a seat number.
func safeGetPlayerBySeatNum(seatNum int32) *Player {
	v, ok := playerMap.Load(seatNum)
	if !ok {
		return nil
	}
	p, ok := v.(*Player)
	if !ok {
		return nil
	}
	// Treat pre-allocated empty slots as "no player".
	if p.UserName == "" {
		return nil
	}
	return p
}

// safeGetTable returns the table for the given table number.
func safeGetTable(tableNum int32) *Table {
	v, ok := tableMap.Load(tableNum)
	if !ok {
		return nil
	}
	t, ok := v.(*Table)
	if !ok {
		return nil
	}
	return t
}

// --------------- Player collection helpers ---------------

// getAllOnlinePlayers returns every player currently in the userName→player map.
func getAllOnlinePlayers() []*Player {
	var players []*Player
	userName2Player.Range(func(_, value interface{}) bool {
		if p, ok := value.(*Player); ok && p != nil {
			players = append(players, p)
		}
		return true
	})
	return players
}

// excludePlayer returns a copy of the slice with one client ID removed.
func excludePlayer(players []*Player, excludeCid *proto.ClientID) []*Player {
	if excludeCid == nil {
		return players
	}
	var result []*Player
	for _, p := range players {
		if p != nil && p.Cid != nil && p.Cid.ID != excludeCid.ID {
			result = append(result, p)
		}
	}
	return result
}

// --------------- Table mutation helpers ---------------

// addPlayerToTable seats a player at the table, assigns seat/cards, and
// updates both the player and table state atomically (from the caller's
// single-goroutine perspective).
func addPlayerToTable(table *Table, player *Player) {
	seatNum := GetSeatNum(table.TableNum, table.PlayerCount)
	player.SeatNum = seatNum
	player.TableNum = table.TableNum
	playerMap.Store(seatNum, player)
	table.Players = append(table.Players, player)
	table.PlayerCount++
	table.Cards = GetRandomCards()
}

// removePlayerFromTable removes a player from the table's Players slice,
// decrements the count and resets round-level flags.
func removePlayerFromTable(table *Table, player *Player) {
	for k, v := range table.Players {
		if v == player {
			table.Players = append(table.Players[:k], table.Players[k+1:]...)
			break
		}
	}
	table.PlayerCount--
	if table.PlayerCount < 0 {
		table.PlayerCount = 0
	}
	table.IsPlay = false
	table.IsGrab = false
	table.IsWait = true
}

// --------------- Hall list helpers ---------------

// rebuildHallList reconstructs the global hallList from tableMap.
// Empty seats (nil players or empty UserName) are filtered out.
func rebuildHallList() {
	hallList = nil
	tableMap.Range(func(key, value interface{}) bool {
		tableNum := key.(int32)
		t := value.(*Table)
		var userNames []string
		for _, p := range t.Players {
			if p != nil && p.UserName != "" {
				userNames = append(userNames, p.UserName)
			}
		}
		hallList = append(hallList, &HallTable{
			TableNum:  tableNum,
			UserNames: userNames,
			IsPlay:    t.IsPlay,
			IsFull:    t.PlayerCount >= 3,
		})
		return true
	})
	sort.Sort(hallList)
}

// --------------- Response builders ---------------

// buildInitHallResponse builds the InitHallResponse payload (jsonType 109).
func buildInitHallResponse() *proto.JsonACK {
	rebuildHallList()
	init := &InitHallResponse{HallTables: hallList}
	data, _ := json.Marshal(init)
	return &proto.JsonACK{JsonType: 109, Content: data}
}

// buildRefreshHallResponse rebuilds the hall list and returns a
// RefreshHallResponse payload (jsonType 114).
func buildRefreshHallResponse() *proto.JsonACK {
	rebuildHallList()
	refresh := &RefreshHallResponse{HallTables: hallList}
	data, _ := json.Marshal(refresh)
	return &proto.JsonACK{JsonType: 114, Content: data}
}

// --------------- Seat / card helpers (unchanged logic) ---------------

// GetSeatNum derives the seat number from table number and current count.
func GetSeatNum(tableNum, tablePlayerCount int32) int32 {
	return (tableNum-1)*3 + tablePlayerCount + 1
}

// GetRandomCards returns a shuffled deck (shuffled three times).
func GetRandomCards() []int32 {
	source := make([]int32, len(CARDS))
	copy(source, CARDS)
	rand.Seed(time.Now().UnixNano())
	for i := 0; i < 3; i++ {
		rand.Shuffle(len(source), func(a, b int) {
			source[a], source[b] = source[b], source[a]
		})
	}
	return source
}

// GetRandomLandlord picks a random starting seat for the landlord round.
func GetRandomLandlord(tableNum int32) int32 {
	return (tableNum-1)*3 + 1 + int32(rand.Float32()*2.0)
}

func GetLeftRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	list := make([]int32, 0, len(players))
	for _, v := range players {
		if v != nil {
			list = append(list, v.SeatNum)
		}
	}
	if len(list) == 0 {
		return 0
	}
	maxSeatNum := max(list)
	if maxSeatNum-yourSeatNum == 0 {
		return maxSeatNum - 1
	} else if maxSeatNum-yourSeatNum == 1 {
		return maxSeatNum - 2
	}
	return maxSeatNum
}

func GetRightRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	list := make([]int32, 0, len(players))
	for _, v := range players {
		if v != nil {
			list = append(list, v.SeatNum)
		}
	}
	if len(list) == 0 {
		return 0
	}
	maxSeatNum := max(list)
	if maxSeatNum-yourSeatNum == 0 {
		return maxSeatNum - 2
	} else if maxSeatNum-yourSeatNum == 1 {
		return maxSeatNum
	}
	return maxSeatNum - 1
}

// RefreshSeatNum2UserName returns a seatNum→userName map for the table.
func RefreshSeatNum2UserName(table *Table) map[int32]string {
	tablePlayers := make(map[int32]string)
	for _, v := range table.Players {
		if v != nil && v.UserName != "" {
			tablePlayers[v.SeatNum] = v.UserName
		}
	}
	return tablePlayers
}

func max(a []int32) int32 {
	m := int32(math.MinInt32)
	for _, v := range a {
		if v > m {
			m = v
		}
	}
	return m
}

func min(a []int32) int32 {
	m := int32(math.MaxInt32)
	for _, v := range a {
		if v < m {
			m = v
		}
	}
	return m
}

// --------------- Broadcast helpers ---------------

// WrapMultiSend sends ack to every non-nil player in the list,
// optionally excluding one client ID.
func WrapMultiSend(players []*Player, ack *proto.JsonACK, cid *proto.ClientID) {
	var cids []*proto.ClientID
	for _, v := range players {
		if v == nil || v.Cid == nil {
			continue
		}
		if cid != nil && v.Cid.ID == cid.ID {
			continue
		}
		cids = append(cids, v.Cid)
	}
	if len(cids) == 0 {
		return
	}
	agentapi.MultipleSend(cids, ack)
}

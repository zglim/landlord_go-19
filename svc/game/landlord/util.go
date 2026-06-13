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

// ---------------------------------------------------------------------------
// 安全加载 helpers：从 sync.Map 取不到时返回 nil，避免 nil 类型断言 panic
// ---------------------------------------------------------------------------

func loadPlayer(cid *proto.ClientID) *Player {
	if cid == nil {
		return nil
	}
	v, ok := clientID2Player.Load(cid.ID)
	if !ok {
		return nil
	}
	return v.(*Player)
}

func loadTable(tableNum int32) *Table {
	v, ok := tableMap.Load(tableNum)
	if !ok {
		return nil
	}
	return v.(*Table)
}

// ---------------------------------------------------------------------------
// 大厅状态构建：每次从 tableMap 实时重建，杜绝增量修改导致的不同步
// ---------------------------------------------------------------------------

func buildHallList() hallTables {
	list := make(hallTables, 0)
	tableMap.Range(func(key, value interface{}) bool {
		tableNum := key.(int32)
		table := value.(*Table)
		var userNames []string
		for _, p := range table.Players {
			if p != nil && p.UserName != "" {
				userNames = append(userNames, p.UserName)
			}
		}
		list = append(list, &HallTable{
			TableNum:  tableNum,
			UserNames: userNames,
			IsPlay:    table.IsPlay,
			IsFull:    table.PlayerCount >= 3,
		})
		return true
	})
	sort.Sort(list)
	return list
}

// ---------------------------------------------------------------------------
// 广播 helpers
// ---------------------------------------------------------------------------

// BroadcastToAllOnline 给所有在线玩家发消息（遍历 userName2Player）
func BroadcastToAllOnline(ack *proto.JsonACK) {
	var players []*Player
	userName2Player.Range(func(key, value interface{}) bool {
		if p := value.(*Player); p != nil && p.Cid != nil {
			players = append(players, p)
		}
		return true
	})
	WrapMultiSend(players, ack, nil)
}

// BroadcastHallRefresh 重建大厅列表并广播给所有在线玩家
func BroadcastHallRefresh() {
	hallList = buildHallList()
	data, _ := json.Marshal(&RefreshHallResponse{hallList})
	agentapi.BroadcastAll(&proto.JsonACK{JsonType: 114, Content: data})
}

// ---------------------------------------------------------------------------
// 原有工具函数
// ---------------------------------------------------------------------------

//根据牌桌号获取座位号
func GetSeatNum(tableNum, tablePlayerCount int32) int32 {
	return (tableNum - 1) * 3 + tablePlayerCount + 1
}

//获取随机牌，牌洗了三遍
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

//随机选地主
func GetRandomLandlord(tableNum int32) int32 {
	return (tableNum - 1) * 3 + 1 + int32(rand.Float32()*2.0)
}

func GetLeftRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	var list []int32
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
	} else {
		return maxSeatNum
	}
}

func GetRightRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	var list []int32
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
	} else {
		return maxSeatNum - 1
	}
}

//返回table中的座位号-用户名map
func RefreshSeatNum2UserName(table *Table) map[int32]string {
	tablePlayers := make(map[int32]string)
	for _, v := range table.Players {
		if v != nil {
			tablePlayers[v.SeatNum] = v.UserName
		}
	}
	return tablePlayers
}

func max(a []int32) int32 {
	var m int32
	m = math.MinInt32
	for _, v := range a {
		if v > m {
			m = v
		}
	}
	return m
}

func min(a []int32) int32 {
	var m int32
	m = math.MaxInt32
	for _, v := range a {
		if v < m {
			m = v
		}
	}
	return m
}

//群发的封装（修复：nil 判断顺序 + 空列表保护）
func WrapMultiSend(players []*Player, ack *proto.JsonACK, excludeCid *proto.ClientID) {
	if len(players) == 0 {
		return
	}
	var cids []*proto.ClientID
	for _, v := range players {
		if v == nil || v.Cid == nil {
			continue
		}
		if excludeCid != nil && v.Cid.ID == excludeCid.ID {
			continue
		}
		cids = append(cids, v.Cid)
	}
	if len(cids) == 0 {
		return
	}
	agentapi.MultipleSend(cids, ack)
}

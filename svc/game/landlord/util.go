package landlord

import (
	"landlord_go/proto"
	"landlord_go/svc/agent/api"
	"math"
	"math/rand"
	"time"
)

// GetSeatNum 根据牌桌号和当前人数计算下一个座位号。
func GetSeatNum(tableNum, tablePlayerCount int32) int32 {
	return (tableNum-1)*3 + tablePlayerCount + 1
}

// GetRandomCards 洗牌（洗三遍）并返回完整的 54 张牌。
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

// GetRandomLandlord 随机选一个地主座位号（在牌桌的 3 个座位中随机选一个）。
func GetRandomLandlord(tableNum int32) int32 {
	return (tableNum-1)*3 + 1 + int32(rand.Float32()*2.0)
}

// GetLeftRivalSeatNum 返回左手边对手的座位号。
func GetLeftRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	maxSeatNum := maxSeat(players)
	switch maxSeatNum - yourSeatNum {
	case 0:
		return maxSeatNum - 1
	case 1:
		return maxSeatNum - 2
	default:
		return maxSeatNum
	}
}

// GetRightRivalSeatNum 返回右手边对手的座位号。
func GetRightRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	maxSeatNum := maxSeat(players)
	switch maxSeatNum - yourSeatNum {
	case 0:
		return maxSeatNum - 2
	case 1:
		return maxSeatNum
	default:
		return maxSeatNum - 1
	}
}

// SeatUserNames 返回牌桌内 座位号→用户名 的映射，用于组 EnterTable/ExitSeat/GrabLandlord 响应。
func SeatUserNames(t *Table) map[int32]string {
	m := make(map[int32]string, len(t.Players))
	for _, p := range t.Players {
		if p != nil {
			m[p.SeatNum] = p.UserName
		}
	}
	return m
}

// RemovePlayerFromTable 将指定玩家从牌桌的 Players 切片中移除，并返回是否找到。
func RemovePlayerFromTable(t *Table, target *Player) bool {
	for i, p := range t.Players {
		if p == target {
			t.Players = append(t.Players[:i], t.Players[i+1:]...)
			return true
		}
	}
	return false
}

// ResetTableState 将牌桌状态重置为等待中。
func ResetTableState(t *Table) {
	t.IsPlay = false
	t.IsGrab = false
	t.IsWait = true
}

// maxSeat 返回牌桌中最大的座位号。
func maxSeat(players []*Player) int32 {
	var m int32 = math.MinInt32
	for _, p := range players {
		if p != nil && p.SeatNum > m {
			m = p.SeatNum
		}
	}
	return m
}

// WrapMultiSend 群发消息给 players 列表中的玩家，可排除一个客户端。
// 修复了旧版本先访问 v.Cid 再判空的问题。
func WrapMultiSend(players []*Player, ack *proto.JsonACK, exclude *proto.ClientID) {
	var cids []*proto.ClientID
	for _, p := range players {
		if p == nil || p.Cid == nil {
			continue
		}
		if exclude != nil && p.Cid.ID == exclude.ID {
			continue
		}
		cids = append(cids, p.Cid)
	}
	if len(cids) == 0 {
		return
	}
	agentapi.MultipleSend(cids, ack)
}

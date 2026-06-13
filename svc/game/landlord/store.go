package landlord

import (
	"fmt"
	"landlord_go/proto"
)

// lookupPlayer 根据 ClientID 安全地查找在线玩家。
// 找不到时返回 error，避免后续 nil panic。
func lookupPlayer(cid *proto.ClientID) (*Player, error) {
	v, ok := clientID2Player.Load(cid.ID)
	if !ok {
		return nil, fmt.Errorf("player not found for cid=%d", cid.ID)
	}
	p, ok := v.(*Player)
	if !ok || p == nil {
		return nil, fmt.Errorf("invalid player entry for cid=%d", cid.ID)
	}
	return p, nil
}

// lookupTable 根据牌桌号安全地查找牌桌。
func lookupTable(tableNum int32) (*Table, error) {
	v, ok := tableMap.Load(tableNum)
	if !ok {
		return nil, fmt.Errorf("table not found: %d", tableNum)
	}
	t, ok := v.(*Table)
	if !ok || t == nil {
		return nil, fmt.Errorf("invalid table entry: %d", tableNum)
	}
	return t, nil
}

// lookupPlayerAndTable 一次性查找玩家及其所在牌桌，是最常用的组合查询。
func lookupPlayerAndTable(cid *proto.ClientID) (*Player, *Table, error) {
	p, err := lookupPlayer(cid)
	if err != nil {
		return nil, nil, err
	}
	t, err := lookupTable(p.TableNum)
	if err != nil {
		return nil, nil, err
	}
	return p, t, nil
}

// getPlayerBySeat 根据座位号安全地查找玩家。
func getPlayerBySeat(seatNum int32) (*Player, error) {
	v, ok := playerMap.Load(seatNum)
	if !ok {
		return nil, fmt.Errorf("player not found for seat=%d", seatNum)
	}
	p, ok := v.(*Player)
	if !ok || p == nil {
		return nil, fmt.Errorf("invalid player entry for seat=%d", seatNum)
	}
	return p, nil
}

// allOnlinePlayers 返回当前所有在线玩家的列表。
func allOnlinePlayers() []*Player {
	var players []*Player
	userName2Player.Range(func(_, value interface{}) bool {
		if p, ok := value.(*Player); ok && p != nil {
			players = append(players, p)
		}
		return true
	})
	return players
}

// storePlayer 将玩家注册到所有相关索引中。
func storePlayer(cid *proto.ClientID, p *Player) {
	clientID2Player.Store(cid.ID, p)
	userName2Player.Store(p.UserName, p)
}

// removePlayer 从所有索引中移除玩家。
func removePlayer(p *Player, cid *proto.ClientID) {
	userName2Player.Delete(p.UserName)
	clientID2Player.Delete(cid.ID)
	playerMap.Delete(p.SeatNum)
}

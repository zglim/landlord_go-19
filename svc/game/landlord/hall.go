package landlord

import (
	"landlord_go/proto"
	"landlord_go/svc/agent/api"
	"sort"
)

// rebuildHallList 从 tableMap 重新构建完整的大厅列表。
// Login 和 InitHall 都需要完整快照，统一收口在这里。
func rebuildHallList() []*HallTable {
	tableMap.Range(func(key, value interface{}) bool {
		tableNum, ok := key.(int32)
		if !ok {
			return true
		}
		t, ok := value.(*Table)
		if !ok || t == nil {
			return true
		}
		var userNames []string
		for _, p := range t.Players {
			if p != nil {
				userNames = append(userNames, p.UserName)
			}
		}
		hallList[tableNum-1] = &HallTable{
			TableNum:  tableNum,
			UserNames: userNames,
			IsPlay:    t.IsPlay,
			IsFull:    t.PlayerCount == 3,
		}
		return true
	})
	sort.Sort(hallList)
	return hallList
}

// updateHallEntry 针对单张牌桌更新大厅条目（进桌 / 准备 / 开局等场景）。
func updateHallEntry(tableNum int32, userNames []string, isPlay, isFull bool) {
	for _, h := range hallList {
		if h.TableNum == tableNum {
			h.UserNames = userNames
			h.IsPlay = isPlay
			h.IsFull = isFull
			return
		}
	}
}

// addHallPlayer 向指定牌桌的大厅条目追加一个用户名。
func addHallPlayer(tableNum int32, userName string) {
	for _, h := range hallList {
		if h.TableNum == tableNum {
			h.UserNames = append(h.UserNames, userName)
			h.IsPlay = false
			h.IsFull = len(h.UserNames) >= 3
			return
		}
	}
}

// removeHallPlayer 从指定牌桌的大厅条目中移除一个用户名。
func removeHallPlayer(tableNum int32, userName string) {
	for _, h := range hallList {
		if h.TableNum == tableNum {
			for i, name := range h.UserNames {
				if name == userName {
					h.UserNames = append(h.UserNames[:i], h.UserNames[i+1:]...)
					break
				}
			}
			h.IsFull = false
			h.IsPlay = false
			return
		}
	}
}

// broadcastRefreshHall 向所有在线客户端广播最新的大厅状态。
func broadcastRefreshHall() {
	list := make([]*HallTable, len(hallList))
	copy(list, hallList)
	sendBroadcast(&RefreshHallResponse{HallTables: list}, 114)
}

// broadcastRefreshHallExcept 向除 exclude 以外的所有在线客户端广播大厅状态。
// EnterTable 场景下需要排除刚进桌的玩家（该玩家会收到 EnterTableResponse）。
func broadcastRefreshHallExcept(exclude *proto.ClientID) {
	refresh := &RefreshHallResponse{HallTables: hallList}
	data, err := marshalJSON(refresh)
	if err != nil {
		log.Errorf("broadcastRefreshHallExcept marshal: %v", err)
		return
	}
	ack := &proto.JsonACK{JsonType: 114, Content: data}
	players := allOnlinePlayers()
	WrapMultiSend(players, ack, exclude)
}

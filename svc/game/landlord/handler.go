package landlord

import (
	"encoding/json"
	"landlord_go/proto"
	"landlord_go/svc/agent/api"
	"landlord_go/svc/game/database"
)

// ---------------------------------------------------------------------------
// 登录：处理重复登录、清理旧会话、初始化大厅
// ---------------------------------------------------------------------------
func Login(req *LoginRequest, cid *proto.ClientID) {
	if req.UserName == "" || req.Password == "" {
		log.Errorln("username or password invalid")
		return
	}

	// 清理同一 UserName 的旧会话（断线重连场景）
	if oldVal, loaded := userName2Player.Load(req.UserName); loaded {
		oldPlayer := oldVal.(*Player)
		if oldPlayer.Cid != nil {
			clientID2Player.Delete(oldPlayer.Cid.ID)
		}
		if oldPlayer.SeatNum > 0 {
			playerMap.Delete(oldPlayer.SeatNum)
		}
	}
	// 清理同一 cid 的旧会话
	if oldVal, loaded := clientID2Player.Load(cid.ID); loaded {
		oldPlayer := oldVal.(*Player)
		if oldPlayer.UserName != "" {
			userName2Player.Delete(oldPlayer.UserName)
		}
	}

	player := &Player{Cid: cid, UserName: req.UserName}
	clientID2Player.Store(cid.ID, player)
	userName2Player.Store(req.UserName, player)

	// 群发进场聊天
	chat := &ChatMsgResponse{ChatFlag: 1, UserName: req.UserName,
		Msg: req.UserName + "骑着母猪大摇大摆溜进游戏室！", TableNum: -1}
	data, _ := json.Marshal(chat)
	agentapi.BroadcastAll(&proto.JsonACK{JsonType: 102, Content: data})

	// 发送大厅初始化状态（从 tableMap 实时重建，避免增量 drift）
	hallList = buildHallList()
	init := &InitHallResponse{HallTables: hallList}
	data, _ = json.Marshal(init)
	agentapi.Send(cid, &proto.JsonACK{JsonType: 109, Content: data})
}

// ---------------------------------------------------------------------------
// 大厅初始化：安全遍历，nil 保护
// ---------------------------------------------------------------------------
func InitHall(_ *InitHallRequest, cid *proto.ClientID) {
	hallList = buildHallList()
	init := &InitHallResponse{HallTables: hallList}
	data, _ := json.Marshal(init)
	agentapi.Send(cid, &proto.JsonACK{JsonType: 109, Content: data})
}

// ---------------------------------------------------------------------------
// 进桌：nil 保护 + 重复进桌拦截 + 大厅原子重建
// ---------------------------------------------------------------------------
func EnterTable(req *EnterTableRequest, cid *proto.ClientID) {
	table := loadTable(req.TableNum)
	if table == nil {
		log.Errorf("EnterTable: table %d not found", req.TableNum)
		return
	}
	player := loadPlayer(cid)
	if player == nil {
		log.Errorf("EnterTable: player for cid %d not found", cid.ID)
		return
	}

	// 已在桌内则拒绝重复进桌
	if player.TableNum > 0 {
		log.Errorf("EnterTable: player %s already at table %d", player.UserName, player.TableNum)
		enterTable := &EnterTableResponse{false, nil}
		data, _ := json.Marshal(enterTable)
		agentapi.Send(cid, &proto.JsonACK{JsonType: 105, Content: data})
		return
	}

	if table.PlayerCount >= 3 {
		enterTable := &EnterTableResponse{false, nil}
		data, _ := json.Marshal(enterTable)
		agentapi.Send(cid, &proto.JsonACK{JsonType: 105, Content: data})
		return
	}

	// 分配座位并入桌
	player.TableNum = req.TableNum
	newSeatNum := GetSeatNum(req.TableNum, table.PlayerCount)
	player.SeatNum = newSeatNum
	playerMap.Store(newSeatNum, player)
	table.Players = append(table.Players, player)
	table.PlayerCount++
	table.Cards = GetRandomCards()
	table.TableNum = req.TableNum

	// 通知桌内玩家
	tablePlayers := RefreshSeatNum2UserName(table)
	enterTable := &EnterTableResponse{true, tablePlayers}
	data, _ := json.Marshal(enterTable)
	WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 105, Content: data}, nil)

	// 重建大厅并广播给所有在线玩家（含自己）
	BroadcastHallRefresh()
}

// ---------------------------------------------------------------------------
// 聊天：修复 ChatFlag==2 时错误地把 *Table 当 *Player 的 bug
// ---------------------------------------------------------------------------
func ChatMsg(req *ChatMsgRequest, cid *proto.ClientID) {
	chatMsg := &ChatMsgResponse{req.ChatFlag, req.UserName, req.Msg, req.TableNum}
	data, _ := json.Marshal(chatMsg)

	var players []*Player
	if req.ChatFlag == 1 {
		// 全服聊天
		userName2Player.Range(func(key, value interface{}) bool {
			if p := value.(*Player); p != nil && p.Cid != nil {
				players = append(players, p)
			}
			return true
		})
	} else if req.ChatFlag == 2 {
		// 桌内聊天：根据发送者找到其所在桌
		player := loadPlayer(cid)
		if player == nil || player.TableNum == 0 {
			return
		}
		table := loadTable(player.TableNum)
		if table == nil {
			return
		}
		players = table.Players
	}
	WrapMultiSend(players, &proto.JsonACK{JsonType: 102, Content: data}, nil)
}

// ---------------------------------------------------------------------------
// 准备：nil 保护 + 重复准备拦截 + 开局重置 ReadyFlag
// ---------------------------------------------------------------------------
func Ready(req *ReadyRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	// 防止重复准备
	if player.ReadyFlag {
		return
	}
	player.ReadyFlag = true
	table.ReadyCount++

	if req.IsReady {
		readyResponse := &ReadyResponse{true}
		data, _ := json.Marshal(readyResponse)
		agentapi.Send(cid, &proto.JsonACK{JsonType: 113, Content: data})
	}

	if table.ReadyCount >= 3 {
		table.IsWait = false
		table.IsGrab = true
		log.Infoln("房间当前准备人数：", table.ReadyCount)

		totalCards := table.Cards
		cardsMap := make(map[int32][]int32)
		cardsMap[0] = totalCards[:17]
		cardsMap[1] = totalCards[17:34]
		cardsMap[2] = totalCards[34:51]
		threeCards := []int32{totalCards[51], totalCards[52], totalCards[53]}
		table.ThreeCards = threeCards

		landlordNum := GetRandomLandlord(table.TableNum)
		var index int32 = 0
		for _, v := range table.Players {
			if v == nil {
				continue
			}
			table.PlayersCardsOut[v.SeatNum] = 17
			grabLandlord := &GrabLandlordResponse{landlordNum,
				RefreshSeatNum2UserName(table), threeCards, cardsMap[index]}
			data, _ := json.Marshal(grabLandlord)
			agentapi.Send(v.Cid, &proto.JsonACK{JsonType: 108, Content: data})
			index++
			v.ReadyFlag = false // 新一局重置准备状态
		}
		table.ReadyCount = 0
	}
}

// ---------------------------------------------------------------------------
// 取消准备：nil 保护 + 重复取消拦截 + ReadyCount 不下溢
// ---------------------------------------------------------------------------
func CancelReady(req *CancelReadyRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	// 防止重复取消 / 计数下溢
	if !player.ReadyFlag {
		return
	}
	player.ReadyFlag = false
	table.ReadyCount--
	if table.ReadyCount < 0 {
		table.ReadyCount = 0
	}

	if req.IsCancelReady {
		cancelReady := &CancelReadyResponse{true}
		data, _ := json.Marshal(cancelReady)
		agentapi.Send(cid, &proto.JsonACK{JsonType: 100, Content: data})
	}
}

// ---------------------------------------------------------------------------
// 放弃抢地主
// ---------------------------------------------------------------------------
func GiveUpLandlord(req *GiveUpLandlordRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	table.PassLandlordCount++
	log.Infoln("累计放弃地主次数：", table.PassLandlordCount)

	giveUpLandlord := &GiveUpLandlordResponse{GetRightRivalSeatNum(req.SeatNum, table.Players)}
	data, _ := json.Marshal(giveUpLandlord)
	WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 107, Content: data}, nil)

	if table.PassLandlordCount >= 3 {
		log.Infoln("三次扔地主自动结束抢地主")
		table.PassLandlordCount = 0
		landlord := GetRightRivalSeatNum(req.SeatNum, table.Players)
		for k := range table.PlayersCardsOut {
			if k == landlord {
				table.PlayersCardsOut[k] = 20
			}
		}
		endGrabLandlord := &EndGrabLandlordResponse{landlord, table.ThreeCards}
		data, _ = json.Marshal(endGrabLandlord)
		WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 104, Content: data}, nil)
	}
}

// ---------------------------------------------------------------------------
// 抢地主跳转加倍
// ---------------------------------------------------------------------------
func EndGrabLandlord(req *EndGrabLandlordRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	landlord := req.MeSeatNum
	for k := range table.PlayersCardsOut {
		if k == landlord {
			table.PlayersCardsOut[k] = 20
		}
	}
	endGrabLandlord := &EndGrabLandlordResponse{landlord, table.ThreeCards}
	data, _ := json.Marshal(endGrabLandlord)
	WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 104, Content: data}, nil)
}

// ---------------------------------------------------------------------------
// 加倍请求
// ---------------------------------------------------------------------------
func LandlordMultipleWager(req *LandlordMultipleWagerRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	table.WagerMultipleNum = req.MultipleNum
	if req.MultipleNum == 1 {
		multipleWager := &MultipleWagerResponse{1}
		data, _ := json.Marshal(multipleWager)
		WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 112, Content: data}, nil)
	} else {
		landlordMultipleWager := &LandlordMultipleWagerResponse{req.MultipleNum}
		data, _ := json.Marshal(landlordMultipleWager)
		WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 110, Content: data}, cid)
	}
}

// ---------------------------------------------------------------------------
// 加倍应答
// ---------------------------------------------------------------------------
func MultipleWager(req *MultipleWagerRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	table.AnswerMultipleNum++
	table.AgreedMultipleResult += req.Agreed

	if table.AnswerMultipleNum >= 2 {
		var result int32 = 1
		if table.AgreedMultipleResult >= 2 {
			result = table.WagerMultipleNum
		}
		multipleWager := &MultipleWagerResponse{result}
		data, _ := json.Marshal(multipleWager)
		WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 112, Content: data}, nil)
		table.AgreedMultipleResult = 0
		table.WagerMultipleNum = 1
		table.AnswerMultipleNum = 0
	}
}

// ---------------------------------------------------------------------------
// 出牌
// ---------------------------------------------------------------------------
func CardsOut(req *CardsOutRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	var cardsOut *CardsOutResponse
	if req.IsPass {
		table.ContinuousPass++
		if table.ContinuousPass >= 2 {
			table.ContinuousPass = 0
			cardsOut = &CardsOutResponse{req.IsPass, true, req.FromSeatNum,
				req.ToSeatNum, table.LastCardsOut, table.ThrowOutCards,
				table.PlayersCardsOut}
		} else {
			cardsOut = &CardsOutResponse{req.IsPass, false, req.FromSeatNum,
				req.ToSeatNum, table.LastCardsOut, table.ThrowOutCards,
				table.PlayersCardsOut}
		}
	} else {
		table.ContinuousPass = 0
		table.LastCardsOut = req.CardsOut
		for k := range req.CardsOut {
			table.ThrowOutCards[req.CardsOut[k]%20]++
		}
		table.PlayersCardsOut[req.FromSeatNum] -= int32(len(req.CardsOut))
		cardsOut = &CardsOutResponse{req.IsPass, false, req.FromSeatNum,
			req.ToSeatNum, req.CardsOut, table.ThrowOutCards,
			table.PlayersCardsOut}
	}
	data, _ := json.Marshal(cardsOut)
	WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 101, Content: data}, nil)
}

// ---------------------------------------------------------------------------
// 结束游戏
// ---------------------------------------------------------------------------
func EndGame(req *EndGameRequest, cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	endGame := &EndGameResponse{req.WinnerSeatNum}
	data, _ := json.Marshal(endGame)
	WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 103, Content: data}, nil)
}

// ---------------------------------------------------------------------------
// 退出牌桌：修复 slice 删除 + 大厅重建
// ---------------------------------------------------------------------------
func ExitSeat(req *ExitSeatRequest, cid *proto.ClientID) {
	val, ok := playerMap.Load(req.YourSeatNum)
	if !ok {
		return
	}
	player := val.(*Player)
	if player == nil || player.UserName == "" {
		return
	}
	table := loadTable(player.TableNum)
	if table == nil {
		return
	}

	// 从桌内移除玩家（安全 slice 删除）
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
	// 重置桌内剩余玩家的准备状态
	for _, v := range table.Players {
		if v != nil {
			v.ReadyFlag = false
		}
	}

	exitSeat := &ExitSeatResponse{player.UserName, req.YourSeatNum,
		RefreshSeatNum2UserName(table)}
	data, _ := json.Marshal(exitSeat)
	WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 106, Content: data}, cid)

	playerMap.Delete(req.YourSeatNum)
	player.TableNum = 0
	player.SeatNum = 0

	// 大厅重建 + 全服广播
	BroadcastHallRefresh()
}

// ---------------------------------------------------------------------------
// 退出大厅
// ---------------------------------------------------------------------------
func ExitHall(req *ExitHallRequest, cid *proto.ClientID) {
	// 清理所有关联 map
	userName2Player.Delete(req.UserName)
	if cid != nil {
		clientID2Player.Delete(cid.ID)
	}
}

// ---------------------------------------------------------------------------
// 获取个人信息
// ---------------------------------------------------------------------------
func UserInfo(req *UserInfoRequest, cid *proto.ClientID) {
	res, err := database.GetUserInfo(req.UserName)
	if err != nil {
		return
	}
	userInfo := &UserInfoResponse{res["name"], res["avatar"], res["win"],
		res["lose"], res["money"]}
	data, _ := json.Marshal(userInfo)
	agentapi.Send(cid, &proto.JsonACK{JsonType: 115, Content: data})
}

// ---------------------------------------------------------------------------
// 游戏结果
// ---------------------------------------------------------------------------
func GameResult(req *GameResultRequest, cid *proto.ClientID) {
	var gameResult *GameResultResponse
	if req.Result {
		res := database.Win(req.UserName, req.Password, req.Money)
		gameResult = &GameResultResponse{res}
	} else {
		res := database.Lose(req.UserName, req.Password, req.Money)
		gameResult = &GameResultResponse{res}
	}
	data, _ := json.Marshal(gameResult)
	agentapi.Send(cid, &proto.JsonACK{JsonType: 116, Content: data})
}

// ---------------------------------------------------------------------------
// 断线/异常退出：修复两处 slice 删除 bug + 大厅重建 + 全量清理
// ---------------------------------------------------------------------------
func ExitOrException(cid *proto.ClientID) {
	player := loadPlayer(cid)
	if player == nil {
		return
	}

	// 清理座位
	if player.SeatNum > 0 {
		playerMap.Delete(player.SeatNum)
	}

	// 如果在桌内，修复桌状态
	if player.TableNum > 0 {
		table := loadTable(player.TableNum)
		if table != nil {
			// 修复：原代码 append(table.Players[k:], table.Players[:k+1]...) 是错的
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
			// 重置桌内剩余玩家的准备状态
			for _, v := range table.Players {
				if v != nil {
					v.ReadyFlag = false
				}
			}

			exitSeat := &ExitSeatResponse{player.UserName,
				player.SeatNum, RefreshSeatNum2UserName(table)}
			data, _ := json.Marshal(exitSeat)
			WrapMultiSend(table.Players, &proto.JsonACK{JsonType: 106, Content: data}, cid)
		}
	}

	// 大厅重建 + 全服广播
	BroadcastHallRefresh()

	// 全量清理
	userName2Player.Delete(player.UserName)
	clientID2Player.Delete(cid.ID)
}

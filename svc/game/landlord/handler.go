package landlord

import (
	"encoding/json"
	"landlord_go/proto"
	"landlord_go/svc/game/database"
)

// Login handles user login: registers the player, broadcasts a join message
// to everyone, and sends the current hall state back to the new player.
func Login(req *LoginRequest, cid *proto.ClientID) {
	if req.UserName == "" || req.Password == "" {
		log.Errorln("username or password invalid")
		return
	}

	player := &Player{Cid: cid, UserName: req.UserName}
	clientID2Player.Store(cid.ID, player)
	userName2Player.Store(req.UserName, player)

	// Broadcast join announcement to all connected clients.
	chat := &ChatMsgResponse{
		ChatFlag:   1,
		UserName:   req.UserName,
		Msg:        req.UserName + "骑着母猪大摇大摆溜进游戏室！",
		TableNum:   -1,
	}
	chatData, _ := json.Marshal(chat)
	broadcastToAll(&proto.JsonACK{JsonType: 102, Content: chatData})

	// Send hall state to the newly logged-in user.
	sendToPlayer(cid, buildInitHallResponse())
}

// InitHall returns the current hall state to the requesting client.
func InitHall(_ *InitHallRequest, cid *proto.ClientID) {
	sendToPlayer(cid, buildInitHallResponse())
}

// EnterTable seats a player at a table. Rejects if the table is full,
// the player is already seated, or the lookup fails.
func EnterTable(req *EnterTableRequest, cid *proto.ClientID) {
	table := safeGetTable(req.TableNum)
	if table == nil {
		log.Errorf("EnterTable: table %d not found", req.TableNum)
		return
	}
	player := safeGetPlayerByCid(cid)
	if player == nil {
		log.Errorf("EnterTable: player for cid %d not found", cid.ID)
		return
	}

	// Guard: reject if the player is already seated at some table.
	if player.TableNum > 0 {
		log.Errorf("EnterTable: player %s already at table %d", player.UserName, player.TableNum)
		sendToPlayer(cid, &proto.JsonACK{
			JsonType: 105,
			Content:  mustMarshal(&EnterTableResponse{IsSuccess: false}),
		})
		return
	}

	if table.PlayerCount >= 3 {
		sendToPlayer(cid, &proto.JsonACK{
			JsonType: 105,
			Content:  mustMarshal(&EnterTableResponse{IsSuccess: false}),
		})
		return
	}

	// Seat the player.
	addPlayerToTable(table, player)
	userName2Player.Store(req.UserName, player)

	// Notify all players at the table about the new seating.
	tablePlayers := RefreshSeatNum2UserName(table)
	enterResp := &EnterTableResponse{IsSuccess: true, TablePlayers: tablePlayers}
	enterData, _ := json.Marshal(enterResp)
	broadcastToTable(table, &proto.JsonACK{JsonType: 105, Content: enterData}, nil)

	// Rebuild hall list and broadcast refresh to ALL online players
	// (including the one who just entered, so their hall view stays current).
	broadcastToHall(buildRefreshHallResponse())
}

// ChatMsg forwards a chat message. ChatFlag 1 = global, 2 = table-scoped.
func ChatMsg(req *ChatMsgRequest, _ *proto.ClientID) {
	chatMsg := &ChatMsgResponse{
		ChatFlag: req.ChatFlag,
		UserName: req.UserName,
		Msg:      req.Msg,
		TableNum: req.TableNum,
	}
	data, _ := json.Marshal(chatMsg)
	ack := &proto.JsonACK{JsonType: 102, Content: data}

	switch req.ChatFlag {
	case 1: // global
		broadcastToHall(ack)
	case 2: // table-scoped
		table := safeGetTable(req.TableNum)
		if table == nil {
			log.Errorf("ChatMsg: table %d not found", req.TableNum)
			return
		}
		broadcastToTable(table, ack, nil)
	}
}

// Ready marks a player as ready. When all three are ready the game starts.
func Ready(req *ReadyRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		log.Errorf("Ready: player for cid %d not found", cid.ID)
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		log.Errorf("Ready: table %d not found", player.TableNum)
		return
	}

	table.ReadyCount++
	if req.IsReady {
		sendToPlayer(cid, &proto.JsonACK{
			JsonType: 113,
			Content:  mustMarshal(&ReadyResponse{Ready: true}),
		})
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
		var index int32
		for _, v := range table.Players {
			if v == nil {
				continue
			}
			table.PlayersCardsOut[v.SeatNum] = 17
			grabLandlord := &GrabLandlordResponse{
				LandlordSeatNum: landlordNum,
				TablePlayers:    RefreshSeatNum2UserName(table),
				ThreeCards:      threeCards,
				Cards:           cardsMap[index],
			}
			data, _ := json.Marshal(grabLandlord)
			sendToPlayer(v.Cid, &proto.JsonACK{JsonType: 108, Content: data})
			index++
		}
		table.ReadyCount = 0
	}
}

// CancelReady decrements the ready counter.
func CancelReady(req *CancelReadyRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		return
	}

	table.ReadyCount--
	if table.ReadyCount < 0 {
		table.ReadyCount = 0
	}

	if req.IsCancelReady {
		sendToPlayer(cid, &proto.JsonACK{
			JsonType: 100,
			Content:  mustMarshal(&CancelReadyResponse{IsCancelReady: true}),
		})
	}
}

// GiveUpLandlord records a pass on the landlord bid. After three passes
// the last candidate automatically becomes landlord.
func GiveUpLandlord(req *GiveUpLandlordRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		return
	}

	table.PassLandlordCount++
	log.Infoln("累计放弃地主次数：", table.PassLandlordCount)

	nextSeat := GetRightRivalSeatNum(req.SeatNum, table.Players)
	giveUp := &GiveUpLandlordResponse{NextLandlordSeatNum: nextSeat}
	data, _ := json.Marshal(giveUp)
	broadcastToTable(table, &proto.JsonACK{JsonType: 107, Content: data}, nil)

	if table.PassLandlordCount >= 3 {
		log.Infoln("三次扔地主自动结束抢地主")
		table.PassLandlordCount = 0
		landlord := GetRightRivalSeatNum(req.SeatNum, table.Players)
		for k := range table.PlayersCardsOut {
			if k == landlord {
				table.PlayersCardsOut[k] = 20
			}
		}
		endGrab := &EndGrabLandlordResponse{
			FinalLandlordSeatNum: landlord,
			ThreeCards:           table.ThreeCards,
		}
		endData, _ := json.Marshal(endGrab)
		broadcastToTable(table, &proto.JsonACK{JsonType: 104, Content: endData}, nil)
	}
}

// EndGrabLandlord finalises the landlord bid (the winner accepts).
func EndGrabLandlord(req *EndGrabLandlordRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		return
	}

	landlord := req.MeSeatNum
	for k := range table.PlayersCardsOut {
		if k == landlord {
			table.PlayersCardsOut[k] = 20
		}
	}

	endGrab := &EndGrabLandlordResponse{
		FinalLandlordSeatNum: landlord,
		ThreeCards:           table.ThreeCards,
	}
	data, _ := json.Marshal(endGrab)
	broadcastToTable(table, &proto.JsonACK{JsonType: 104, Content: data}, nil)
}

// LandlordMultipleWager handles the landlord's wager multiplier proposal.
func LandlordMultipleWager(req *LandlordMultipleWagerRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		return
	}

	table.WagerMultipleNum = req.MultipleNum
	if req.MultipleNum == 1 {
		data, _ := json.Marshal(&MultipleWagerResponse{MultipleNum: 1})
		broadcastToTable(table, &proto.JsonACK{JsonType: 112, Content: data}, nil)
	} else {
		data, _ := json.Marshal(&LandlordMultipleWagerResponse{MultipleNum: req.MultipleNum})
		broadcastToTable(table, &proto.JsonACK{JsonType: 110, Content: data}, cid)
	}
}

// MultipleWager handles the farmers' response to a landlord wager proposal.
func MultipleWager(req *MultipleWagerRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		return
	}
	table := safeGetTable(player.TableNum)
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
		data, _ := json.Marshal(&MultipleWagerResponse{MultipleNum: result})
		broadcastToTable(table, &proto.JsonACK{JsonType: 112, Content: data}, nil)
		table.AgreedMultipleResult = 0
		table.WagerMultipleNum = 1
		table.AnswerMultipleNum = 0
	}
}

// CardsOut handles a play or pass during a round.
func CardsOut(req *CardsOutRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		return
	}

	var cardsOut *CardsOutResponse
	if req.IsPass {
		table.ContinuousPass++
		allPass := table.ContinuousPass >= 2
		if allPass {
			table.ContinuousPass = 0
		}
		cardsOut = &CardsOutResponse{
			IsPass:            req.IsPass,
			IsAllPass:         allPass,
			FromSeatNum:       req.FromSeatNum,
			ToSeatNum:         req.ToSeatNum,
			CardsOut:          table.LastCardsOut,
			ThrowOutCards:     table.ThrowOutCards,
			PlayersCardsCount: table.PlayersCardsOut,
		}
	} else {
		table.ContinuousPass = 0
		table.LastCardsOut = req.CardsOut
		for _, c := range req.CardsOut {
			table.ThrowOutCards[c%20]++
		}
		table.PlayersCardsOut[req.FromSeatNum] -= int32(len(req.CardsOut))
		cardsOut = &CardsOutResponse{
			IsPass:            req.IsPass,
			IsAllPass:         false,
			FromSeatNum:       req.FromSeatNum,
			ToSeatNum:         req.ToSeatNum,
			CardsOut:          req.CardsOut,
			ThrowOutCards:     table.ThrowOutCards,
			PlayersCardsCount: table.PlayersCardsOut,
		}
	}

	data, _ := json.Marshal(cardsOut)
	broadcastToTable(table, &proto.JsonACK{JsonType: 101, Content: data}, nil)
}

// EndGame announces the round winner to the table.
func EndGame(req *EndGameRequest, cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		return
	}

	data, _ := json.Marshal(&EndGameResponse{WinnerSeatNum: req.WinnerSeatNum})
	broadcastToTable(table, &proto.JsonACK{JsonType: 103, Content: data}, nil)
}

// ExitSeat handles a voluntary seat exit. The departing player receives
// no ExitSeat message (they initiated it); everyone else at the table does.
// A RefreshHall is broadcast to all online players.
func ExitSeat(req *ExitSeatRequest, cid *proto.ClientID) {
	player := safeGetPlayerBySeatNum(req.YourSeatNum)
	if player == nil {
		log.Errorf("ExitSeat: player at seat %d not found", req.YourSeatNum)
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		log.Errorf("ExitSeat: table %d not found", player.TableNum)
		return
	}

	// Notify remaining table players (exclude the one leaving).
	exitSeat := &ExitSeatResponse{
		UserName:     player.UserName,
		SeatNum:      req.YourSeatNum,
		TablePlayers: RefreshSeatNum2UserName(table),
	}
	exitData, _ := json.Marshal(exitSeat)
	broadcastToTable(table, &proto.JsonACK{JsonType: 106, Content: exitData}, cid)

	// Remove after broadcasting so the notification targets the right set.
	removePlayerFromTable(table, player)

	// Broadcast refreshed hall to everyone.
	broadcastToAll(buildRefreshHallResponse())

	// Clear the seat slot (keep the pre-allocated key for reuse).
	playerMap.Store(req.YourSeatNum, &Player{})
}

// ExitHall removes the user from the online player map.
func ExitHall(req *ExitHallRequest, _ *proto.ClientID) {
	userName2Player.Delete(req.UserName)
}

// UserInfo fetches and returns user profile data from the database.
func UserInfo(req *UserInfoRequest, cid *proto.ClientID) {
	res, err := database.GetUserInfo(req.UserName)
	if err != nil {
		return
	}
	userInfo := &UserInfoResponse{
		Name:   res["name"],
		Avatar: res["avatar"],
		Win:    res["win"],
		Lose:   res["lose"],
		Money:  res["money"],
	}
	data, _ := json.Marshal(userInfo)
	sendToPlayer(cid, &proto.JsonACK{JsonType: 115, Content: data})
}

// GameResult persists a win/loss and returns the new balance.
func GameResult(req *GameResultRequest, cid *proto.ClientID) {
	var res int32
	if req.Result {
		res = database.Win(req.UserName, req.Password, req.Money)
	} else {
		res = database.Lose(req.UserName, req.Password, req.Money)
	}
	data, _ := json.Marshal(&GameResultResponse{Status: res})
	sendToPlayer(cid, &proto.JsonACK{JsonType: 116, Content: data})
}

// ExitOrException handles an unexpected disconnect. Remaining table players
// are notified, the hall is refreshed, and all player mappings are cleaned up.
func ExitOrException(cid *proto.ClientID) {
	player := safeGetPlayerByCid(cid)
	if player == nil {
		log.Errorf("ExitOrException: player for cid %d not found", cid.ID)
		return
	}
	table := safeGetTable(player.TableNum)
	if table == nil {
		// Player was in the lobby but not at a table; still clean up maps.
		userName2Player.Delete(player.UserName)
		clientID2Player.Delete(cid.ID)
		return
	}

	// Notify remaining table players (exclude the disconnecting one).
	exitSeat := &ExitSeatResponse{
		UserName:     player.UserName,
		SeatNum:      player.SeatNum,
		TablePlayers: RefreshSeatNum2UserName(table),
	}
	exitData, _ := json.Marshal(exitSeat)
	broadcastToTable(table, &proto.JsonACK{JsonType: 106, Content: exitData}, cid)

	// Now remove the player from the table.
	removePlayerFromTable(table, player)

	// Broadcast refreshed hall to everyone.
	broadcastToAll(buildRefreshHallResponse())

	// Clean up all maps.
	playerMap.Store(player.SeatNum, &Player{}) // reset slot for reuse
	userName2Player.Delete(player.UserName)
	clientID2Player.Delete(cid.ID)
}

// --------------- internal helpers ---------------

// mustMarshal is a convenience wrapper that marshals v to JSON, returning
// an empty slice on error (should never happen for our response structs).
func mustMarshal(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}

package landlord

import (
	"landlord_go/proto"
	"landlord_go/svc/game/database"
)

// ---------------------------------------------------------------------------
// Handler conventions
//
// Every handler follows the same three-phase structure:
//
//   1. Validate  — check request fields and look up required state.
//                  Return early on error; nothing is mutated.
//   2. Mutate    — change Table / Player / global map state.
//   3. Respond   — marshal and send response(s).
//
// Shared helpers live in errors.go (lookups), broadcast.go (messaging /
// hall assembly), table.go (table mutations) and util.go (pure functions).
// ---------------------------------------------------------------------------

// Login validates credentials, registers the player, broadcasts a chat
// announcement, and sends the caller a hall snapshot.
func Login(req *LoginRequest, cid *proto.ClientID) {
	// 1. Validate
	if req == nil || req.UserName == "" || req.Password == "" {
		log.Errorln("login: username or password invalid")
		return
	}
	if cid == nil {
		log.Errorln("login: nil client id")
		return
	}

	// 2. Mutate
	player := &Player{Cid: cid, UserName: req.UserName}
	clientID2Player.Store(cid.ID, player)
	userName2Player.Store(req.UserName, player)

	// 3. Respond — announce arrival, then send hall snapshot.
	marshalBroadcast(102, &ChatMsgResponse{
		ChatFlag: 1,
		UserName: req.UserName,
		Msg:      req.UserName + "骑着母猪大摇大摆溜进游戏室！",
		TableNum: -1,
	})
	sendHallInit(cid)
}

// InitHall returns the hall snapshot to a client that just connected to the
// hall view. No mutation.
func InitHall(_ *InitHallRequest, cid *proto.ClientID) {
	if cid == nil {
		return
	}
	sendHallInit(cid)
}

// EnterTable seats a player at a table, notifies everyone at the table, and
// refreshes the hall for every other online player.
func EnterTable(req *EnterTableRequest, cid *proto.ClientID) {
	// 1. Validate
	if req == nil {
		return
	}
	table, err := loadTableByNum(req.TableNum)
	if err != nil {
		log.Errorln("enterTable:", err)
		return
	}
	player, err := loadPlayerByCID(cid)
	if err != nil {
		log.Errorln("enterTable:", err)
		return
	}
	if table.PlayerCount >= MaxPlayersPerTable {
		marshalSend(cid, 105, &EnterTableResponse{IsSuccess: false})
		return
	}

	// 2. Mutate
	addPlayerToTable(table, player)
	userName2Player.Store(req.UserName, player)

	// 3. Respond
	marshalMultiSend(table.Players, 105, &EnterTableResponse{
		IsSuccess:    true,
		TablePlayers: seatUserNameMap(table),
	}, nil)
	broadcastHallRefresh(cid)
}

// ChatMsg forwards a chat message. ChatFlag=1 is hall-wide; ChatFlag=2 is
// table-local (note: the original table-local path iterated tableMap which
// holds *Table, not *Player, so it never reached anyone — left as-is with
// a TODO so behavior is unchanged).
func ChatMsg(req *ChatMsgRequest, _ *proto.ClientID) {
	if req == nil {
		return
	}
	resp := &ChatMsgResponse{
		ChatFlag: req.ChatFlag,
		UserName: req.UserName,
		Msg:      req.Msg,
		TableNum: req.TableNum,
	}
	var players []*Player
	switch req.ChatFlag {
	case 1: // hall
		players = allOnlinePlayers()
	case 2: // table — TODO: original code was broken; kept for behavior parity.
		tableMap.Range(func(_, value interface{}) bool {
			if p, ok := value.(*Player); ok {
				players = append(players, p)
			}
			return true
		})
	}
	marshalMultiSend(players, 102, resp, nil)
}

// Ready marks the caller as ready. When all 3 seats are ready the round
// begins: cards are dealt, a random landlord is chosen, and every player
// receives a GrabLandlordResponse.
func Ready(req *ReadyRequest, cid *proto.ClientID) {
	// 1. Validate
	if req == nil {
		return
	}
	player, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("ready:", err)
		return
	}
	_ = player

	// 2. Mutate
	table.ReadyCount++
	if req.IsReady {
		marshalSend(cid, 113, &ReadyResponse{Ready: true})
	}

	// 3. Start the round if the table is full and everyone is ready.
	if table.ReadyCount == MaxPlayersPerTable {
		startGrabPhase(table)
	}
}

// startGrabPhase deals cards, picks a random landlord and notifies every
// player. Extracted from Ready so the "3 players ready → game start" rule
// stays easy to find.
func startGrabPhase(table *Table) {
	table.IsWait = false
	table.IsGrab = true
	log.Infoln("startGrabPhase: room ready count =", table.ReadyCount)

	// Deal 17 cards each, keep 3 aside as the landlord's bonus.
	total := table.Cards
	dealt := [3][]int32{total[:17], total[17:34], total[34:51]}
	three := append([]int32(nil), total[51], total[52], total[53])
	table.ThreeCards = three

	landlordSeat := GetRandomLandlord(table.TableNum)

	// Initialize per-seat card counter for this round.
	for _, p := range table.Players {
		table.PlayersCardsOut[p.SeatNum] = 17
	}

	seatMap := seatUserNameMap(table)
	for i, p := range table.Players {
		marshalSend(p.Cid, 108, &GrabLandlordResponse{
			LandlordSeatNum: landlordSeat,
			TablePlayers:    seatMap,
			ThreeCards:      three,
			Cards:           dealt[i],
		})
	}
	table.ReadyCount = 0
}

// CancelReady decrements the ready counter and confirms to the caller.
func CancelReady(req *CancelReadyRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	_, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("cancelReady:", err)
		return
	}
	table.ReadyCount--
	if req.IsCancelReady {
		marshalSend(cid, 100, &CancelReadyResponse{IsCancelReady: true})
	}
}

// GiveUpLandlord records one more pass. On the third consecutive pass the
// landlord is auto-assigned to the last player's right-hand rival and the
// grab phase ends.
func GiveUpLandlord(req *GiveUpLandlordRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	_, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("giveUpLandlord:", err)
		return
	}
	table.PassLandlordCount++
	log.Infoln("giveUpLandlord: pass count =", table.PassLandlordCount)

	nextSeat := GetRightRivalSeatNum(req.SeatNum, table.Players)
	marshalMultiSend(table.Players, 107, &GiveUpLandlordResponse{
		NextLandlordSeatNum: nextSeat,
	}, nil)

	if table.PassLandlordCount == MaxPlayersPerTable {
		finalizeLandlord(table, nextSeat)
	}
}

// EndGrabLandlord is called by the player who decides to claim the landlord
// role. The grab phase ends immediately.
func EndGrabLandlord(req *EndGrabLandlordRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	_, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("endGrabLandlord:", err)
		return
	}
	finalizeLandlord(table, req.MeSeatNum)
}

// finalizeLandlord is the single path that transitions from grab phase to
// the wagering / play phase. Called both when someone claims landlord and
// when three consecutive passes auto-assign it.
func finalizeLandlord(table *Table, landlordSeat int32) {
	log.Infoln("finalizeLandlord: seat =", landlordSeat)
	table.PassLandlordCount = 0
	assignLandlordCards(table, landlordSeat)
	marshalMultiSend(table.Players, 104, &EndGrabLandlordResponse{
		FinalLandlordSeatNum: landlordSeat,
		ThreeCards:           table.ThreeCards,
	}, nil)
}

// LandlordMultipleWager broadcasts the landlord's proposed multiplier. A
// value of 1 means "no raise" and is sent as a final MultipleWagerResponse;
// anything higher is sent as a LandlordMultipleWagerResponse to the two
// farmers for approval.
func LandlordMultipleWager(req *LandlordMultipleWagerRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	_, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("landlordMultipleWager:", err)
		return
	}
	table.WagerMultipleNum = req.MultipleNum
	if req.MultipleNum == 1 {
		marshalMultiSend(table.Players, 112, &MultipleWagerResponse{MultipleNum: 1}, nil)
		return
	}
	marshalMultiSend(table.Players, 110, &LandlordMultipleWagerResponse{
		MultipleNum: req.MultipleNum,
	}, cid)
}

// MultipleWager is a farmer's accept/reject response to a landlord raise.
// Once both farmers have answered, the final multiplier is broadcast.
func MultipleWager(req *MultipleWagerRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	_, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("multipleWager:", err)
		return
	}
	table.AnswerMultipleNum++
	table.AgreedMultipleResult += req.Agreed

	if table.AnswerMultipleNum != 2 {
		return
	}
	final := int32(1)
	if table.AgreedMultipleResult >= 2 {
		final = table.WagerMultipleNum
	}
	marshalMultiSend(table.Players, 112, &MultipleWagerResponse{MultipleNum: final}, nil)
	// Reset multiplier state for the next round.
	table.AgreedMultipleResult = 0
	table.WagerMultipleNum = 1
	table.AnswerMultipleNum = 0
}

// CardsOut handles a player playing cards (or passing).
func CardsOut(req *CardsOutRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	_, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("cardsOut:", err)
		return
	}

	resp := buildCardsOutResponse(table, req)
	marshalMultiSend(table.Players, 101, resp, nil)
}

// buildCardsOutResponse applies the pass / play rules to the table state
// and returns the response. Split out so CardsOut stays a thin handler.
func buildCardsOutResponse(table *Table, req *CardsOutRequest) *CardsOutResponse {
	if req.IsPass {
		table.ContinuousPass++
		allPass := table.ContinuousPass >= 2
		if allPass {
			table.ContinuousPass = 0
		}
		return &CardsOutResponse{
			IsPass:            true,
			IsAllPass:         allPass,
			FromSeatNum:       req.FromSeatNum,
			ToSeatNum:         req.ToSeatNum,
			CardsOut:          table.LastCardsOut,
			ThrowOutCards:     table.ThrowOutCards,
			PlayersCardsCount: table.PlayersCardsOut,
		}
	}
	table.ContinuousPass = 0
	table.LastCardsOut = req.CardsOut
	for _, c := range req.CardsOut {
		table.ThrowOutCards[c%20]++
	}
	table.PlayersCardsOut[req.FromSeatNum] -= int32(len(req.CardsOut))
	return &CardsOutResponse{
		IsPass:            false,
		IsAllPass:         false,
		FromSeatNum:       req.FromSeatNum,
		ToSeatNum:         req.ToSeatNum,
		CardsOut:          req.CardsOut,
		ThrowOutCards:     table.ThrowOutCards,
		PlayersCardsCount: table.PlayersCardsOut,
	}
}

// EndGame announces the winner to everyone at the table.
func EndGame(req *EndGameRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	_, table, err := loadPlayerAndTable(cid)
	if err != nil {
		log.Errorln("endGame:", err)
		return
	}
	marshalMultiSend(table.Players, 103, &EndGameResponse{
		WinnerSeatNum: req.WinnerSeatNum,
	}, nil)
}

// ExitSeat lets a player voluntarily leave their seat.
func ExitSeat(req *ExitSeatRequest, cid *proto.ClientID) {
	if req == nil {
		return
	}
	player, err := loadPlayerBySeatNum(req.YourSeatNum)
	if err != nil {
		log.Errorln("exitSeat:", err)
		return
	}
	table, err := loadTableByNum(player.TableNum)
	if err != nil {
		log.Errorln("exitSeat:", err)
		return
	}

	// Mutate
	removePlayerFromTable(table, player)
	table.PlayerCount--
	resetTableForExit(table)

	// Respond
	marshalMultiSend(table.Players, 106, &ExitSeatResponse{
		UserName:     player.UserName,
		SeatNum:      req.YourSeatNum,
		TablePlayers: seatUserNameMap(table),
	}, cid)
	playerMap.Delete(req.YourSeatNum)
	broadcastHallRefresh(nil)
}

// ExitHall just removes the username → player mapping.
func ExitHall(req *ExitHallRequest, _ *proto.ClientID) {
	if req == nil {
		return
	}
	userName2Player.Delete(req.UserName)
}

// UserInfo queries the DB for the caller's profile and sends it back.
func UserInfo(req *UserInfoRequest, cid *proto.ClientID) {
	if req == nil || cid == nil {
		return
	}
	res, err := database.GetUserInfo(req.UserName)
	if err != nil {
		log.Errorln("userInfo:", err)
		return
	}
	marshalSend(cid, 115, &UserInfoResponse{
		Name:   res["name"],
		Avatar: res["avatar"],
		Win:    res["win"],
		Lose:   res["lose"],
		Money:  res["money"],
	})
}

// GameResult persists a win or loss and returns the status code.
func GameResult(req *GameResultRequest, cid *proto.ClientID) {
	if req == nil || cid == nil {
		return
	}
	status := int32(database.UPDATE_SERVER_ERROR)
	if req.Result {
		status = database.Win(req.UserName, req.Password, req.Money)
	} else {
		status = database.Lose(req.UserName, req.Password, req.Money)
	}
	marshalSend(cid, 116, &GameResultResponse{Status: status})
}

// ExitOrException is invoked when a client disconnects unexpectedly. It
// performs the same seat cleanup as ExitSeat plus removes the client and
// username mappings.
func ExitOrException(cid *proto.ClientID) {
	player, err := loadPlayerByCID(cid)
	if err != nil {
		log.Errorln("exitOrException:", err)
		return
	}
	table, err := loadTableByNum(player.TableNum)
	if err != nil {
		// Even if the table is gone we still need to drop the mappings.
		cleanupPlayerMappings(player, cid)
		return
	}

	seat := player.SeatNum
	removePlayerFromTable(table, player)
	table.PlayerCount--
	resetTableForExit(table)

	marshalMultiSend(table.Players, 106, &ExitSeatResponse{
		UserName:     player.UserName,
		SeatNum:      seat,
		TablePlayers: seatUserNameMap(table),
	}, cid)
	playerMap.Delete(seat)
	broadcastHallRefresh(nil)
	cleanupPlayerMappings(player, cid)
}

// cleanupPlayerMappings removes the global username / cid entries for a
// player that has fully left.
func cleanupPlayerMappings(p *Player, cid *proto.ClientID) {
	userName2Player.Delete(p.UserName)
	clientID2Player.Delete(cid.ID)
}

// loadPlayerBySeatNum is a small convenience used only by ExitSeat which
// starts from a seat number rather than a CID.
func loadPlayerBySeatNum(seat int32) (*Player, error) {
	v, ok := playerMap.Load(seat)
	if !ok {
		return nil, ErrPlayerNotFound
	}
	p, ok := v.(*Player)
	if !ok || p == nil {
		return nil, ErrPlayerNotFound
	}
	return p, nil
}

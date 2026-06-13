package landlord

import (
	"landlord_go/proto"
	"landlord_go/svc/agent/api"
)

// Mockable notification function variables.
// Production defaults delegate to agentapi; tests override them to record calls.
var (
	notifySend         func(cid *proto.ClientID, ack *proto.JsonACK)
	notifyBroadcastAll func(ack *proto.JsonACK)
	notifyMultiSend    func(players []*Player, ack *proto.JsonACK, excludeCid *proto.ClientID)
)

func init() {
	notifySend = func(cid *proto.ClientID, ack *proto.JsonACK) {
		agentapi.Send(cid, ack)
	}
	notifyBroadcastAll = func(ack *proto.JsonACK) {
		agentapi.BroadcastAll(ack)
	}
	notifyMultiSend = WrapMultiSend
}

// sendToPlayer sends a single message to one player.
func sendToPlayer(cid *proto.ClientID, ack *proto.JsonACK) {
	if cid == nil {
		return
	}
	notifySend(cid, ack)
}

// broadcastToAll sends a message to every connected client.
func broadcastToAll(ack *proto.JsonACK) {
	notifyBroadcastAll(ack)
}

// broadcastToTable sends ack to every player at the table, optionally
// excluding one client ID.
func broadcastToTable(table *Table, ack *proto.JsonACK, excludeCid *proto.ClientID) {
	if table == nil {
		return
	}
	notifyMultiSend(table.Players, ack, excludeCid)
}

// broadcastToHall sends ack to every online player.
func broadcastToHall(ack *proto.JsonACK) {
	players := getAllOnlinePlayers()
	if len(players) == 0 {
		return
	}
	notifyMultiSend(players, ack, nil)
}

// broadcastToHallExcluding sends ack to every online player except one.
func broadcastToHallExcluding(ack *proto.JsonACK, excludeCid *proto.ClientID) {
	players := getAllOnlinePlayers()
	filtered := excludePlayer(players, excludeCid)
	if len(filtered) == 0 {
		return
	}
	notifyMultiSend(filtered, ack, nil)
}

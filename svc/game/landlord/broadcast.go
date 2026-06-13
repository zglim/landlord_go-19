package landlord

import (
	"encoding/json"
	"landlord_go/proto"
	agentapi "landlord_go/svc/agent/api"
	"sort"
)

// ---------------------------------------------------------------------------
// Sender abstraction
//
// The handler layer used to call agentapi.Send / BroadcastAll / MultipleSend
// directly. That coupling makes the package impossible to test without a
// running cellnet environment. We introduce a small Sender interface so the
// production path still talks to agentapi while tests can plug in a recorder.
// ---------------------------------------------------------------------------

// Sender is the minimal outbound-messaging contract needed by handlers.
type Sender interface {
	Send(cid *proto.ClientID, jsonType int32, data []byte)
	BroadcastAll(jsonType int32, data []byte)
	MultipleSend(cids []*proto.ClientID, jsonType int32, data []byte)
}

// agentSender is the production Sender that forwards to agentapi.
type agentSender struct{}

func (agentSender) Send(cid *proto.ClientID, jsonType int32, data []byte) {
	agentapi.Send(cid, &proto.JsonACK{JsonType: jsonType, Content: data})
}
func (agentSender) BroadcastAll(jsonType int32, data []byte) {
	agentapi.BroadcastAll(&proto.JsonACK{JsonType: jsonType, Content: data})
}
func (agentSender) MultipleSend(cids []*proto.ClientID, jsonType int32, data []byte) {
	if len(cids) == 0 {
		return
	}
	agentapi.MultipleSend(cids, &proto.JsonACK{JsonType: jsonType, Content: data})
}

// sender is the package-level Sender used by every handler. Tests may swap it
// via SetSender and restore with RestoreSender.
var sender Sender = agentSender{}

// SetSender replaces the active sender. Returns the previous one so callers
// can restore it with defer.
func SetSender(s Sender) Sender {
	prev := sender
	sender = s
	return prev
}

// ---------------------------------------------------------------------------
// Marshal + send helpers
//
// Every handler was repeating json.Marshal + agentapi.Send + JsonACK wrapping.
// These helpers collapse the boilerplate and centralize the error path.
// ---------------------------------------------------------------------------

// marshalSend serializes payload and sends it to a single client.
func marshalSend(cid *proto.ClientID, jsonType int32, payload interface{}) {
	if cid == nil {
		log.Errorln("marshalSend: nil cid for jsonType", jsonType)
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Errorf("marshalSend jsonType=%d marshal error: %s", jsonType, err)
		return
	}
	sender.Send(cid, jsonType, data)
}

// marshalBroadcast serializes payload and broadcasts to every online client.
func marshalBroadcast(jsonType int32, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Errorf("marshalBroadcast jsonType=%d marshal error: %s", jsonType, err)
		return
	}
	sender.BroadcastAll(jsonType, data)
}

// marshalMultiSend serializes payload and sends it to a list of players,
// optionally excluding one client (used when the caller already got its own
// tailored response).
func marshalMultiSend(players []*Player, jsonType int32, payload interface{}, exclude *proto.ClientID) {
	cids := collectCIDs(players, exclude)
	if len(cids) == 0 {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Errorf("marshalMultiSend jsonType=%d marshal error: %s", jsonType, err)
		return
	}
	sender.MultipleSend(cids, jsonType, data)
}

// collectCIDs flattens a player slice into a CID slice, skipping nils and
// an optional excluded CID. The previous WrapMultiSend had a nil-check bug
// (checked v.Cid.ID before checking v != nil) — fixed here.
func collectCIDs(players []*Player, exclude *proto.ClientID) []*proto.ClientID {
	out := make([]*proto.ClientID, 0, len(players))
	for _, p := range players {
		if p == nil || p.Cid == nil {
			continue
		}
		if exclude != nil && p.Cid.ID == exclude.ID {
			continue
		}
		out = append(out, p.Cid)
	}
	return out
}

// ---------------------------------------------------------------------------
// Hall list assembly
//
// The hall-list snapshot was duplicated between Login and InitHall, and the
// mutation path (update one entry after enter/exit) was duplicated between
// EnterTable, ExitSeat and ExitOrException. The helpers below make each of
// those call sites a one-liner.
// ---------------------------------------------------------------------------

// buildHallList walks tableMap and returns a sorted snapshot suitable for
// InitHallResponse / RefreshHallResponse. It does NOT touch the cached
// hallList variable — callers that want to refresh the cache use
// refreshHallCache.
func buildHallList() []*HallTable {
	// We don't know the max table number ahead of time; collect into a slice.
	list := make([]*HallTable, 0)
	tableMap.Range(func(key, value interface{}) bool {
		num, ok := key.(int32)
		if !ok {
			return true
		}
		t, ok := value.(*Table)
		if !ok || t == nil {
			return true
		}
		userNames := make([]string, 0, len(t.Players))
		for _, p := range t.Players {
			if p != nil {
				userNames = append(userNames, p.UserName)
			}
		}
		list = append(list, &HallTable{
			TableNum:  num,
			UserNames: userNames,
			IsPlay:    t.IsPlay,
			IsFull:    t.PlayerCount >= 3,
		})
		return true
	})
	sort.Sort(hallTables(list))
	return list
}

// refreshHallCache rebuilds the package-level hallList cache. Called any time
// we are about to broadcast a RefreshHallResponse so every observer sees the
// same snapshot.
func refreshHallCache() {
	hallList = buildHallList()
}

// sendHallInit sends an InitHallResponse (jsonType 109) to a single client.
// Used by both Login and InitHall.
func sendHallInit(cid *proto.ClientID) {
	refreshHallCache()
	marshalSend(cid, 109, &InitHallResponse{HallTables: hallList})
}

// broadcastHallRefresh rebuilds the cache and broadcasts a RefreshHallResponse
// (jsonType 114) to every online player, optionally excluding one CID.
func broadcastHallRefresh(exclude *proto.ClientID) {
	refreshHallCache()
	all := allOnlinePlayers()
	if exclude == nil {
		marshalBroadcast(114, &RefreshHallResponse{HallTables: hallList})
		return
	}
	// Exclude the caller (e.g. the player who just entered a table, they
	// already received an EnterTableResponse).
	marshalMultiSend(all, 114, &RefreshHallResponse{HallTables: hallList}, exclude)
}

// allOnlinePlayers returns a slice of every player currently tracked by
// userName2Player. Used when we need to broadcast to "everyone in the hall".
func allOnlinePlayers() []*Player {
	out := make([]*Player, 0)
	userName2Player.Range(func(_, value interface{}) bool {
		if p, ok := value.(*Player); ok && p != nil {
			out = append(out, p)
		}
		return true
	})
	return out
}

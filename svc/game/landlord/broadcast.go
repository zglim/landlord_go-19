package landlord

import (
	"encoding/json"
	"landlord_go/proto"
	"landlord_go/svc/agent/api"
)

// marshalJSON 统一 JSON 序列化入口，出错时打日志但返回空切片，
// 避免每个 handler 里都写 `data, _ := json.Marshal(...)` 。
func marshalJSON(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Errorf("marshalJSON: %v (payload=%+v)", err, v)
	}
	return data, err
}

// sendToClient 将任意响应结构体序列化后发给单个客户端。
func sendToClient(cid *proto.ClientID, jsonType int32, resp interface{}) {
	data, err := marshalJSON(resp)
	if err != nil {
		return
	}
	agentapi.Send(cid, &proto.JsonACK{JsonType: jsonType, Content: data})
}

// sendToTable 将消息序列化后发给牌桌上的所有玩家。
func sendToTable(t *Table, jsonType int32, resp interface{}) {
	data, err := marshalJSON(resp)
	if err != nil {
		return
	}
	WrapMultiSend(t.Players, &proto.JsonACK{JsonType: jsonType, Content: data}, nil)
}

// sendToTableExcept 将消息发给牌桌上除 exclude 以外的玩家。
func sendToTableExcept(t *Table, jsonType int32, resp interface{}, exclude *proto.ClientID) {
	data, err := marshalJSON(resp)
	if err != nil {
		return
	}
	WrapMultiSend(t.Players, &proto.JsonACK{JsonType: jsonType, Content: data}, exclude)
}

// sendBroadcast 将消息广播给所有在线客户端。
func sendBroadcast(resp interface{}, jsonType int32) {
	data, err := marshalJSON(resp)
	if err != nil {
		return
	}
	agentapi.BroadcastAll(&proto.JsonACK{JsonType: jsonType, Content: data})
}

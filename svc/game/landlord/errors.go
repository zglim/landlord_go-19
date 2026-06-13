package landlord

import (
	"errors"
	"landlord_go/proto"
)

// Sentinel errors returned by safe-lookup helpers.
// Handlers use these to short-circuit when required state is missing instead
// of panicking on nil pointers (the previous default behavior).
var (
	ErrPlayerNotFound = errors.New("player not found")
	ErrTableNotFound  = errors.New("table not found")
	ErrTableFull      = errors.New("table is full")
)

// loadPlayerByCID looks up a Player by its client ID.
// Returns ErrPlayerNotFound when the mapping has not been established yet
// (e.g. the client sent a request before completing Login).
func loadPlayerByCID(cid *proto.ClientID) (*Player, error) {
	if cid == nil {
		return nil, ErrPlayerNotFound
	}
	v, ok := clientID2Player.Load(cid.ID)
	if !ok {
		return nil, ErrPlayerNotFound
	}
	p, ok := v.(*Player)
	if !ok || p == nil {
		return nil, ErrPlayerNotFound
	}
	return p, nil
}

// loadTableByNum looks up a Table by its table number.
func loadTableByNum(tableNum int32) (*Table, error) {
	v, ok := tableMap.Load(tableNum)
	if !ok {
		return nil, ErrTableNotFound
	}
	t, ok := v.(*Table)
	if !ok || t == nil {
		return nil, ErrTableNotFound
	}
	return t, nil
}

// loadPlayerAndTable is a convenience that chains the two lookups above.
// It is the most common combination in handlers that operate on the caller's
// current table (Ready, GiveUpLandlord, CardsOut, ...).
func loadPlayerAndTable(cid *proto.ClientID) (*Player, *Table, error) {
	p, err := loadPlayerByCID(cid)
	if err != nil {
		return nil, nil, err
	}
	t, err := loadTableByNum(p.TableNum)
	if err != nil {
		return nil, nil, err
	}
	return p, t, nil
}

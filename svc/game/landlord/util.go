package landlord

import (
	"math"
	"math/rand"
	"time"
)

// Pure utility functions that do not touch global state. Anything that needs
// to read/write sync.Maps or talk to the network belongs in errors.go,
// broadcast.go or table.go.

// GetSeatNum computes the absolute seat number for the next player joining
// a table. Seats are globally unique across all tables.
func GetSeatNum(tableNum, tablePlayerCount int32) int32 {
	return (tableNum-1)*3 + tablePlayerCount + 1
}

// GetRandomCards returns a shuffled deck. The deck is shuffled three times
// to match the original behavior.
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

// GetRandomLandlord picks a random starting landlord seat within the table's
// seat range.
func GetRandomLandlord(tableNum int32) int32 {
	return (tableNum-1)*3 + 1 + int32(rand.Float32()*2.0)
}

// GetLeftRivalSeatNum returns the seat number of the player sitting to the
// left of yourSeatNum at a 3-seat table.
func GetLeftRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	list := make([]int32, 0, len(players))
	for _, v := range players {
		if v != nil {
			list = append(list, v.SeatNum)
		}
	}
	maxSeatNum := max(list)
	switch maxSeatNum - yourSeatNum {
	case 0:
		return maxSeatNum - 1
	case 1:
		return maxSeatNum - 2
	default:
		return maxSeatNum
	}
}

// GetRightRivalSeatNum returns the seat number of the player sitting to the
// right of yourSeatNum at a 3-seat table.
func GetRightRivalSeatNum(yourSeatNum int32, players []*Player) int32 {
	list := make([]int32, 0, len(players))
	for _, v := range players {
		if v != nil {
			list = append(list, v.SeatNum)
		}
	}
	maxSeatNum := max(list)
	switch maxSeatNum - yourSeatNum {
	case 0:
		return maxSeatNum - 2
	case 1:
		return maxSeatNum
	default:
		return maxSeatNum - 1
	}
}

func max(a []int32) int32 {
	m := int32(math.MinInt32)
	for _, v := range a {
		if v > m {
			m = v
		}
	}
	return m
}

func min(a []int32) int32 {
	m := int32(math.MaxInt32)
	for _, v := range a {
		if v < m {
			m = v
		}
	}
	return m
}

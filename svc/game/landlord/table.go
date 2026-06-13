package landlord

// Table-level helpers. Everything that mutates a Table's player list or
// resets its per-round state lives here so handlers stay readable and the
// same invariants apply everywhere.

// MaxPlayersPerTable is the fixed seat count for a landlord table.
const MaxPlayersPerTable int32 = 3

// seatUserNameMap returns the seat-number → user-name mapping for a table.
// It is the single source of truth for the map carried inside
// EnterTableResponse / ExitSeatResponse / GrabLandlordResponse.
func seatUserNameMap(t *Table) map[int32]string {
	out := make(map[int32]string, len(t.Players))
	for _, p := range t.Players {
		if p != nil {
			out[p.SeatNum] = p.UserName
		}
	}
	return out
}

// addPlayerToTable seats a player at the next free slot on the table,
// updates the global playerMap and returns the assigned seat number.
// Caller is responsible for checking capacity (Table.PlayerCount < 3) first.
func addPlayerToTable(t *Table, p *Player) int32 {
	seat := GetSeatNum(t.TableNum, t.PlayerCount)
	p.TableNum = t.TableNum
	p.SeatNum = seat
	playerMap.Store(seat, p)
	t.Players = append(t.Players, p)
	t.PlayerCount++
	// Re-deal on every seat so the latest entrant always has a full deck
	// available for the upcoming round. This mirrors the original behavior.
	t.Cards = GetRandomCards()
	return seat
}

// removePlayerFromTable removes a specific player from the table's player
// slice. No-op if the player is not present. Does NOT adjust global maps —
// callers combine this with the appropriate cleanup.
func removePlayerFromTable(t *Table, p *Player) {
	for i, v := range t.Players {
		if v == p {
			t.Players = append(t.Players[:i], t.Players[i+1:]...)
			return
		}
	}
}

// resetTableForExit clears the per-round flags that should not survive a
// player leaving mid-game. Shared by ExitSeat and ExitOrException.
func resetTableForExit(t *Table) {
	t.IsPlay = false
	t.IsGrab = false
	t.IsWait = true
}

// assignLandlordCards sets the landlord's card count to 20 (17 + 3) in the
// per-seat counter. Used when the landlord is finalized either by a player
// claiming it or by three consecutive passes.
func assignLandlordCards(t *Table, landlordSeat int32) {
	for seat := range t.PlayersCardsOut {
		if seat == landlordSeat {
			t.PlayersCardsOut[seat] = 20
		}
	}
}

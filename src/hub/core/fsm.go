package core

import (
	"sync"
	"sync/atomic"
	"time"
)

import (
	"misc/timer"
	. "types"
)

// OFFLINE|RAID, OFFLINE|PROTECTED , OFFLINE
// ONLINE|PROTECTED , ONLINE
const (
	HISHIFT = 16

	//
	OFFLINE = 1 << HISHIFT
	ONLINE  = 1 << 1 << HISHIFT

	// battle status
	RAID      = 1
	PROTECTED = 1 << 1

	LOMASK = 0xFFFF
	HIMASK = 0xFFFF0000
)

const (
	RAID_TIME = 300 // seconds
	EVENT_MAX = 50000
)

//--------------------------------------------------------- player info
type PlayerInfo struct {
	Id             int32
	State          int32
	ProtectTimeout int64      // unix time
	RaidTimeout    int64      // unix time
	Host           int32      // host
	WaitEventId    int32      // current waiting event id, a user will only wait on ONE timeout event,  PROTECTTIMEOUT of RAID TIMEOUT
	_lock          sync.Mutex // Record lock
}

func (p *PlayerInfo) Lock() {
	p._lock.Lock()
}

func (p *PlayerInfo) Unlock() {
	p._lock.Unlock()
}

/**********************************************************
 * consider following deadlock situations.
 *
 * A(B) means lock A,lock B, unlock B, unlock A
 * A->B means lockA unlockA,then lockB, unlockB
 *
 * p:A(B), q:B(A), possible circular wait, deadlock!!!
 * p:A(B), q:A(B), ok
 * p.A(B), q:B or A, ok
 * p:A->B, q: B->A, ok
 *
 * make sure acquiring the lock IN SEQUENCE. i.e.
 * A: players
 * B: playerinfo.LCK
 **********************************************************/
var (
	_lock_players sync.RWMutex          // lock players
	_players      map[int32]*PlayerInfo // all players

	_protects     map[int32]*PlayerInfo
	_protectslock sync.Mutex
	_protects_ch  chan int32

	_raids     map[int32]*PlayerInfo
	_raidslock sync.Mutex
	_raids_ch  chan int32

	_event_id_gen int32
)

func init() {
	_players = make(map[int32]*PlayerInfo)
	_protects = make(map[int32]*PlayerInfo)
	_protects_ch = make(chan int32, EVENT_MAX)
	_raids = make(map[int32]*PlayerInfo)
	_raids_ch = make(chan int32, EVENT_MAX)

	go _expire()
}

//--------------------------------------------------------- expires
func _expire() {
	for {
		select {
		case event_id := <-_protects_ch:
			_protectslock.Lock()
			player := _protects[event_id]
			delete(_protects, event_id)
			_protectslock.Unlock()

			if player != nil {
				player.Lock()
				if player.WaitEventId == event_id { // check if it is the waiting event, or just ignore
					player.State = player.State & (^PROTECTED)
				}
				player.Unlock()
			}

		case event_id := <-_raids_ch:
			_raidslock.Lock()
			player := _raids[event_id]
			delete(_raids, event_id)
			_raidslock.Unlock()

			if player != nil {
				player.Lock()
				if player.WaitEventId == event_id {
					player.State = player.State & (^RAID)
				}
				player.Unlock()
			}
		}
	}
}

//------------------------------------------------ add a user to finite state machine manager
func _add_fsm(user *User) {
	player := &PlayerInfo{Id: user.Id, State: OFFLINE}

	if user.ProtectTimeout > time.Now().Unix() {
		player.ProtectTimeout = user.ProtectTimeout
		player.State |= PROTECTED
	}

	_lock_players.Lock()
	_players[user.Id] = player
	_lock_players.Unlock()
}

// The State Machine Of Player
//----------------------------------------------- OFFLINE->ONLINE
// A->B
func Login(id, host int32) bool {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		defer player.Unlock()
		state := player.State

		if state&OFFLINE != 0 && state&RAID == 0 { // when offline & not being raid
			player.State = int32(ONLINE | (state & LOMASK))
			player.Host = host
			return true
		}
	}

	return false
}

//----------------------------------------------- ONLINE->OFFLINE
// A->B
func Logout(id int32) bool {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		defer player.Unlock()

		state := player.State

		if state&ONLINE != 0 { // when online
			player.State = int32(OFFLINE | (state & LOMASK))
			return true
		}
	}

	return false
}

//----------------------------------------------- (OFFLINE|FREE)->RAID
// A->B->A
func Raid(id int32) bool {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()

		state := player.State

		if state&OFFLINE != 0 && state&(RAID|PROTECTED) == 0 { // when offline and free
			timeout := time.Now().Unix() + RAID_TIME

			event_id := atomic.AddInt32(&_event_id_gen, 1)
			timer.Add(event_id, timeout, _raids_ch) // generate timer

			player.State = int32(OFFLINE | RAID)
			player.RaidTimeout = timeout
			player.WaitEventId = event_id
			player.Unlock()

			_raidslock.Lock()
			_raids[event_id] = player
			_raidslock.Unlock()
			return true
		}

		player.Unlock()
	}

	return false
}

//----------------------------------------------- RAID->FREE
// A->B
func Free(id int32) bool {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		defer player.Unlock()

		if player.State&RAID != 0 { // when being raid
			player.State = int32(OFFLINE)
			return true
		}
	}

	return false
}

//----------------------------------------------- PROTECT
// A->B->A
func Protect(id int32, until int64) bool {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		if player.State&RAID == 0 { // when not being raid
			event_id := atomic.AddInt32(&_event_id_gen, 1)
			timer.Add(event_id, until, _raids_ch)

			player.State |= PROTECTED
			player.ProtectTimeout = until
			player.WaitEventId = event_id
			player.Unlock()

			_protectslock.Lock()
			_protects[event_id] = player
			_protectslock.Unlock()
			return true
		}
		player.Unlock()
	}

	return false
}

//----------------------------------------------- UNPROTECT
// A->B->A
func UnProtect(id int32) bool {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		if player.State&PROTECTED != 0 {
			player.State &= ^PROTECTED
			player.Unlock()
			return true
		}
		player.Unlock()
	}

	return false
}

// State Readers
// A->B
func State(id int32) (ret int32) {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		ret = player.State
		player.Unlock()
	}
	return
}

func ProtectTimeout(id int32) (ret int64) {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		ret = player.ProtectTimeout
		player.Unlock()
	}

	return
}

func Host(id int32) (ret int32) {
	_lock_players.RLock()
	player := _players[id]
	_lock_players.RUnlock()

	if player != nil {
		player.Lock()
		ret = player.Host
		player.Unlock()
	}

	return
}

package types

import (
	"types/estate"
	"types/grid"
)

type Session struct {
	MQ            chan interface{} // Player's Internal Message Queue
	Basic         *Basic           //Basic Info
	Res           *Res             // Resource table
	Bitmap        *grid.Grid
	EstateManager *estate.Manager
	Moves         []estate.Move
	HeartBeat     int64
	IsLoggedOut   bool // represents user logout or connection failure
}

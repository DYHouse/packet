package domain

import "github.com/cashparty/backend/common/message"

func MapLuaError(code int) *message.Error {
	switch code {
	case LuaSuccess:
		return nil
	case LuaErrRoomNotFound:
		return message.NewError(message.CodeRoomNotFound)
	case LuaErrRoomFull:
		return message.NewError(message.CodeRoomFull)
	case LuaErrSpectatorFull:
		return message.NewError(message.CodeRoomFull)
	case LuaErrAlreadyInRoom:
		return message.NewError(message.CodeUserAlreadyInRoom)
	case LuaErrNoCandidate:
		return message.NewError(message.CodeNoIdleRoom)
	case LuaErrGameStarted:
		return message.NewError(message.CodeGameInProgress)
	case LuaErrSeatOccupied:
		return message.NewError(message.CodeSeatOccupied)
	case LuaErrInvalidSeat:
		return message.NewError(message.CodeInvalidSeatNo)
	case LuaErrAlreadyPlayer:
		return message.NewError(message.CodeAlreadyPlayer)
	case LuaErrAlreadySeated:
		return message.NewError(message.CodeAlreadySeated)
	case LuaErrNotSeated:
		return message.NewError(message.CodeNotSeated)
	case LuaErrNeedSeatFirst:
		return message.NewError(message.CodeNeedSeatFirst)
	case LuaErrUserNotInRoom:
		return message.NewError(message.CodeNotInRoom)
	case LuaErrTotalFull:
		return message.NewError(message.CodeRoomFull)
	case LuaErrPlayerCannotLeave:
		return message.NewError(message.CodePlayerCannotLeave)
	case LuaErrPacketExists:
		return message.NewError(message.CodePacketExists)
	case LuaErrAlreadyGrabbed:
		return message.NewError(message.CodeAlreadyGrabbed)
	case LuaErrNoPacket:
		return message.NewError(message.CodeNoPacket)
	case LuaErrPacketNotFound:
		return message.NewError(message.CodePacketNotFound)
	case LuaErrPlayerNotFound:
		return message.NewError(message.CodeNotInRoom)
	case LuaErrPlayerNotOffline:
		return message.NewError(message.CodePlayerNotOffline)
	case LuaErrNotPlayer:
		return message.NewError(message.CodeNotPlayer)
	case LuaErrInvalidRoundNumber:
		return message.NewError(message.CodeInvalidRoundNumber)
	case LuaErrNotYourTurn:
		return message.NewError(message.CodeNotYourTurn)
	case LuaErrNoPlayers:
		return message.NewError(message.CodeNoPlayers)
	default:
		return message.NewError(message.CodeSystemError)
	}
}

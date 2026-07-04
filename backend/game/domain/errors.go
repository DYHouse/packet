package domain

import "github.com/cashparty/backend/common/message"

func MapLuaError(code int) *message.Error {
	switch code {
	case LuaErrSuccess:
		return nil
	case LuaErrIdempotent:
		return nil
	case LuaErrRoomNotFound:
		return message.NewError(message.CodeRoomNotFound)
	case LuaErrRoomFull:
		return message.NewError(message.CodeRoomFull)
	case LuaErrRoomFullTotal:
		return message.NewError(message.CodeRoomFull)
	case LuaErrAlreadyInRoom:
		return message.NewError(message.CodeUserAlreadyInRoom)
	case LuaErrNoCandidate:
		return message.NewError(message.CodeNoIdleRoom)
	case LuaErrGameNotInPlaying:
		return message.NewError(message.CodeGameInProgress)
	case LuaErrSeatOccupied:
		return message.NewError(message.CodeSeatOccupied)
	case LuaErrInvalidSeatNo:
		return message.NewError(message.CodeInvalidSeatNo)
	case LuaErrAlreadyPlayer:
		return message.NewError(message.CodeAlreadyPlayer)
	case LuaErrAlreadySeated:
		return message.NewError(message.CodeAlreadySeated)
	case LuaErrNotSeated:
		return message.NewError(message.CodeNotSeated)
	case LuaErrNoSeatSelected:
		return message.NewError(message.CodeNeedSeatFirst)
	case LuaErrNotInRoom:
		return message.NewError(message.CodeNotInRoom)
	case LuaErrPlayerCannotLeave:
		return message.NewError(message.CodePlayerCannotLeave)
	case LuaErrPacketsAlreadyExist:
		return message.NewError(message.CodePacketExists)
	case LuaErrAlreadyGrabbed:
		return message.NewError(message.CodeAlreadyGrabbed)
	case LuaErrPacketNotAvailable:
		return message.NewError(message.CodeNoPacket)
	case LuaErrPacketInfoNotFound:
		return message.NewError(message.CodePacketNotFound)
	case LuaErrPlayerNotFound:
		return message.NewError(message.CodeNotInRoom)
	case LuaErrPlayerNotOffline:
		return message.NewError(message.CodePlayerNotOffline)
	case LuaErrNotInGrabbingPhase:
		return message.NewError(message.CodeNotInGrabbingPhase)
	case LuaErrGrabTimeout:
		return message.NewError(message.CodeGrabTimeout)
	case LuaErrNotFirstRound:
		return message.NewError(message.CodeInvalidRoundNumber)
	case LuaErrNotYourTurn:
		return message.NewError(message.CodeNotYourTurn)
	case LuaErrNoPlayers:
		return message.NewError(message.CodeNoPlayers)
	case LuaErrOnlyPlayerCanGrab:
		return message.NewError(message.CodeNotPlayer)
	case LuaErrAlreadyQueued:
		return message.NewError(message.CodeAlreadyQueued)
	case LuaErrNotQueued:
		return message.NewError(message.CodeNotQueued)
	case LuaErrRobotNotAllowed:
		return message.NewError(message.CodeRobotNotAllowed)
	case LuaErrNoEmptySeat:
		return message.NewError(message.CodeRoomFull)
	case LuaErrSubstituteFail:
		return message.NewError(message.CodeReplacementFailed)
	default:
		return message.NewError(message.CodeSystemError)
	}
}

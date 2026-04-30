package domain

const (
	SenderTypePlayer       = "player"
	SenderTypeSystem       = "system"
	SenderTypeSystemResume = "system_resume"
	SenderTypeSystemForced = "system_forced"
)

type SendScenario int

const (
	SendScenarioFirstRound      SendScenario = 1
	SendScenarioPlayerManual    SendScenario = 2
	SendScenarioTimeoutForced   SendScenario = 3
	SendScenarioResumeInterrupt SendScenario = 4
	SendScenarioLeopardReward   SendScenario = 5
)

func (s SendScenario) SenderType() string {
	switch s {
	case SendScenarioFirstRound:
		return SenderTypeSystem
	case SendScenarioPlayerManual:
		return SenderTypePlayer
	case SendScenarioTimeoutForced:
		return SenderTypeSystemForced
	case SendScenarioResumeInterrupt:
		return SenderTypeSystemResume
	case SendScenarioLeopardReward:
		return SenderTypeSystem
	default:
		return SenderTypePlayer
	}
}

func (s SendScenario) IsSystemSend() bool {
	return s == SendScenarioFirstRound || s == SendScenarioResumeInterrupt || s == SendScenarioLeopardReward
}

func (s SendScenario) SenderID(userID string) string {
	if s.IsSystemSend() {
		return "0"
	}
	return userID
}

func (s SendScenario) SenderNickname(userNickname string) string {
	if s.IsSystemSend() {
		return "system"
	}
	return userNickname
}

type SendPacketParams struct {
	RoomID       string
	SenderID     string
	Scenario     SendScenario
	TotalAmount  int64
	RoundNo      int
	RoundID      int64
	PlayerCount  int
}

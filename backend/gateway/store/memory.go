package store

import (
	"fmt"
	"sync"

	"github.com/cashparty/backend/gateway/model"
)

type MemoryGameStore struct {
	games   []*model.Game
	gameMap map[int]*model.Game
	codeMap map[string]*model.Game
	mu      sync.RWMutex
}

func NewMemoryGameStore() *MemoryGameStore {
	store := &MemoryGameStore{
		games:   make([]*model.Game, 0),
		gameMap: make(map[int]*model.Game),
		codeMap: make(map[string]*model.Game),
	}

	store.initSampleData()
	return store
}

func (s *MemoryGameStore) GetAllGames() ([]*model.Game, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	games := make([]*model.Game, len(s.games))
	copy(games, s.games)
	return games, nil
}

func (s *MemoryGameStore) GetGameByCode(gameCode string) (*model.Game, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	game, exists := s.codeMap[gameCode]
	if !exists {
		return nil, fmt.Errorf("game not found with code: %s", gameCode)
	}
	return game, nil
}

func (s *MemoryGameStore) GetGameByID(gameID int) (*model.Game, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	game, exists := s.gameMap[gameID]
	if !exists {
		return nil, fmt.Errorf("game not found with id: %d", gameID)
	}
	return game, nil
}

func (s *MemoryGameStore) AddGame(game *model.Game) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.games = append(s.games, game)
	s.gameMap[game.ID] = game
	s.codeMap[game.GameCode] = game
	return nil
}

func (s *MemoryGameStore) initSampleData() {
	sampleGames := []*model.Game{
		{
			ID:         1,
			Name:       "redpacket",
			GameCode:   "redpacket",
			Category:   "bonus",
			Provider:   "sd",
			ResourceID: 1,
			CoverURL:   "https://static.example.com/img/lucky_tanks@2x.png",
			Status:     int(model.GameStatusActive),
		},
	}

	for _, game := range sampleGames {
		s.AddGame(game)
	}
}

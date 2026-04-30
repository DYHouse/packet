package handler

import (
	"errors"
	"net/http"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/gateway/model"
	"github.com/cashparty/backend/gateway/service"
	"github.com/gin-gonic/gin"
)

type GameHandler struct {
	gameService *service.GameService
	gameStore   GameStore
}

type GameStore interface {
	GetAllGames() ([]*model.Game, error)
	GetGameByCode(gameCode string) (*model.Game, error)
	GetGameByID(gameID int) (*model.Game, error)
}

func NewGameHandler(gameService *service.GameService, store GameStore) *GameHandler {
	return &GameHandler{
		gameService: gameService,
		gameStore:   store,
	}
}

type GameListRequest struct {
	Page int `form:"page" binding:"omitempty,min=1"`
}

func (h *GameHandler) GetGameList(c *gin.Context) {
	var req GameListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Warn("invalid game list request", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "Invalid request parameters",
			"data": nil,
		})
		return
	}

	ctx := c.Request.Context()

	games, err := h.gameStore.GetAllGames()
	if err != nil {
		logger.Error("failed to get games from store", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "Failed to get game list",
			"data": nil,
		})
		return
	}

	gameList := h.gameService.GetGameList(ctx, games)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "",
		"data": gameList,
	})
}

func (h *GameHandler) StartGame(c *gin.Context) {
	var req service.GameStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("invalid game start request", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "Invalid request parameters: " + err.Error(),
			"data": nil,
		})
		return
	}

	ctx := c.Request.Context()

	var game *model.Game
	var err error

	if req.GameCode != "" {
		game, err = h.gameStore.GetGameByCode(req.GameCode)
	} else if req.GameID > 0 {
		game, err = h.gameStore.GetGameByID(req.GameID)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "game_code or game_id is required",
			"data": nil,
		})
		return
	}

	if err != nil {
		logger.Error("failed to get game", "error", err, "game_code", req.GameCode, "game_id", req.GameID)
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "Game not found",
			"data": nil,
		})
		return
	}

	resp, err := h.gameService.StartGame(ctx, &req, game)
	if err != nil {
		logger.Error("failed to start game", "error", err)
		if errors.Is(err, service.ErrUserSaveFailed) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "Unauthorized: " + err.Error(),
				"data": nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "Failed to start game: " + err.Error(),
			"data": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "",
		"data": resp,
	})
}

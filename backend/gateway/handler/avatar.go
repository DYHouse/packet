package handler

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/gateway/config"
	"github.com/cashparty/backend/gateway/middleware"
	"github.com/gin-gonic/gin"
)

// AvatarHandler 头像上传 handler。
// 鉴权由 JWTAuthMiddleware 完成（注入 internal_user_id 到 context）。
// 落盘路径：{UploadDir}/{internalUserID}_{unix_ms}.{ext}
// 返回 URL：{PublicBaseURL}/{filename}
type AvatarHandler struct {
	cfg *config.AvatarUploadConfig
}

func NewAvatarHandler(cfg *config.AvatarUploadConfig) *AvatarHandler {
	return &AvatarHandler{cfg: cfg}
}

// magicBytes 各图片格式的文件头特征。
var magicBytes = map[string][]byte{
	"image/png":  {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
	"image/jpeg": {0xFF, 0xD8, 0xFF},
	"image/webp": {0x52, 0x49, 0x46, 0x46}, // "RIFF"
}

// webpTrailer WebP 文件偏移 8-11 字节固定为 "WEBP"。
var webpTrailer = []byte("WEBP")

// extByType content-type → 扩展名映射。
var extByType = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/webp": "webp",
}

// UploadAvatar 处理 POST /avatar/upload。
// 请求：multipart/form-data, field "file"
// 响应：{ "code": 0, "data": { "avatar_url": "https://..." } }
func (h *AvatarHandler) UploadAvatar(c *gin.Context) {
	internalUserID := c.GetString(middleware.ContextKeyInternalUserID)
	if internalUserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "missing user id"})
		return
	}

	if h.cfg == nil || h.cfg.UploadDir == "" || h.cfg.PublicBaseURL == "" {
		logger.Error("avatar upload config missing")
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "avatar upload not configured"})
		return
	}

	// 1. 限制请求体大小
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.cfg.MaxSizeBytes)

	// 2. 解析 multipart
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "failed to read file: " + err.Error()})
		return
	}
	defer file.Close()

	// 3. 校验类型（content-type 白名单 + magic bytes 双重校验，防伪造）
	ext, err := h.validateFile(header, file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": err.Error()})
		return
	}

	// 4. 落盘
	filename := fmt.Sprintf("%s_%d.%s", internalUserID, time.Now().UnixMilli(), ext)
	dst := filepath.Join(h.cfg.UploadDir, filename)
	if err := c.SaveUploadedFile(header, dst); err != nil {
		logger.Error("failed to save avatar", "error", err, "path", dst)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "failed to save file"})
		return
	}

	// 5. 返回 URL
	avatarURL := h.cfg.PublicBaseURL + "/" + filename
	logger.Info("avatar uploaded", "user_id", internalUserID, "path", dst, "url", avatarURL)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{"avatar_url": avatarURL},
	})
}

// validateFile 双重校验文件类型：content-type 白名单 + magic bytes。
// 返回扩展名（不含 .），如 "png" / "jpg" / "webp"。
func (h *AvatarHandler) validateFile(header *multipart.FileHeader, file multipart.File) (string, error) {
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		return "", errors.New("missing content-type")
	}

	// 白名单校验
	allowed := false
	for _, t := range h.cfg.AllowedTypes {
		if t == contentType {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("unsupported content-type: %s", contentType)
	}

	// magic bytes 校验
	expected, ok := magicBytes[contentType]
	if !ok {
		return "", fmt.Errorf("unsupported content-type: %s", contentType)
	}

	// 读取文件头（最多 12 字节，覆盖 PNG/JPEG/WebP）
	buf := make([]byte, 12)
	n, err := file.Read(buf)
	if err != nil || n < len(expected) {
		return "", errors.New("failed to read file header")
	}
	for i, b := range expected {
		if buf[i] != b {
			return "", errors.New("file content does not match declared type (magic bytes mismatch)")
		}
	}
	// WebP 额外校验偏移 8-11 = "WEBP"
	if contentType == "image/webp" {
		if n < 12 || string(buf[8:12]) != string(webpTrailer) {
			return "", errors.New("invalid webp file header")
		}
	}

	// 复位文件读指针，SaveUploadedFile 会重新读取
	if _, err := file.Seek(0, 0); err != nil {
		return "", fmt.Errorf("failed to seek file: %w", err)
	}

	ext, ok := extByType[contentType]
	if !ok {
		return "", fmt.Errorf("no extension mapping for content-type: %s", contentType)
	}
	return ext, nil
}

package utils

import (
	"errors"
	"time"

	"video-feed/config"

	"github.com/golang-jwt/jwt/v5"
)

// Claims JWT 载荷：user_id + 登录态版本号。
// 昵称等展示信息按 user_id 查库，不放进 token（避免签发时的快照过期）。
type Claims struct {
	UserID  uint `json:"user_id"`
	Version int  `json:"ver"`
	jwt.RegisteredClaims
}

// GenerateToken 签发 JWT（登录成功时调用）。
// version 是用户当前的 token_version；退出登录会递增版本，旧 token 立即失效。
func GenerateToken(cfg *config.JWTConfig, userID uint, version int) (string, error) {
	claims := Claims{
		UserID:  userID,
		Version: version,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(cfg.Expire) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "video-feed",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(cfg.Secret))
}

// ParseToken 解析并校验 JWT（认证中间件每次请求调用）
func ParseToken(cfg *config.JWTConfig, tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		// 校验签名算法，防止算法混淆攻击
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(cfg.Secret), nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}

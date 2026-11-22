package utils

import (
	"testing"
	"time"

	"video-feed/config"

	"github.com/golang-jwt/jwt/v5"
)

func testCfg(hours int) *config.JWTConfig {
	return &config.JWTConfig{Secret: "unit-test-secret", Expire: hours}
}

func TestGenerateAndParse(t *testing.T) {
	cfg := testCfg(24)
	token, err := GenerateToken(cfg, 7, 3)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	claims, err := ParseToken(cfg, token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if claims.UserID != 7 {
		t.Fatalf("UserID = %d, want 7", claims.UserID)
	}
	if claims.Version != 3 {
		t.Fatalf("Version = %d, want 3", claims.Version)
	}
	if claims.ExpiresAt == nil || time.Until(claims.ExpiresAt.Time) <= 0 {
		t.Fatal("exp 应该在未来")
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	cfg := testCfg(-1) // 签发时就已经过期
	token, err := GenerateToken(cfg, 1, 0)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if _, err := ParseToken(testCfg(24), token); err == nil {
		t.Fatal("过期 token 必须报错")
	}
}

func TestWrongSecretRejected(t *testing.T) {
	token, err := GenerateToken(testCfg(24), 1, 0)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	other := &config.JWTConfig{Secret: "another-secret", Expire: 24}
	if _, err := ParseToken(other, token); err == nil {
		t.Fatal("错误密钥必须验签失败")
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	token, err := GenerateToken(testCfg(24), 1, 0)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	tampered := token[:len(token)-2] + "xx"
	if _, err := ParseToken(testCfg(24), tampered); err == nil {
		t.Fatal("被篡改的 token 必须报错")
	}
}

func TestNonHMACAlgorithmRejected(t *testing.T) {
	cfg := testCfg(24)
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, &Claims{UserID: 1})
	noneToken, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("构造 none token 失败: %v", err)
	}
	if _, err := ParseToken(cfg, noneToken); err == nil {
		t.Fatal("alg=none 必须被算法闸口拒绝")
	}
}

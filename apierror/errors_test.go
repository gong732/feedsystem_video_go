package apierror

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func runWrite(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	Write(c, err)
	return w
}

func TestWriteBusinessError(t *testing.T) {
	w := runWrite(t, BadRequest("参数错误"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if body := w.Body.String(); body == "" {
		t.Fatal("响应体不能为空")
	}
}

func TestWriteRecordNotFound(t *testing.T) {
	w := runWrite(t, gorm.ErrRecordNotFound)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestWriteUnknownError(t *testing.T) {
	w := runWrite(t, gorm.ErrInvalidDB)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

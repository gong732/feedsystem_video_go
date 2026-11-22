package handlers

import (
	"video-feed/apierror"
	"video-feed/service"

	"github.com/gin-gonic/gin"
)

type createCommentReq struct {
	Content string `json:"content" binding:"required"`
}

// CreateComment 发布评论。
func CreateComment(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req createCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apierror.BadRequest("评论内容不能为空"))
		return
	}
	cm, err := service.Comments.Create(c.Request.Context(), c.GetUint("user_id"), id, req.Content)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "评论成功", "data": cm})
}

// ListComments 评论列表（id 倒序游标分页）。
func ListComments(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var cursor int64
	if v := queryInt64(c, "cursor"); v > 0 {
		cursor = v
	}
	page, err := service.Comments.List(c.Request.Context(), id, cursor, queryLimit(c, 10, 50))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": page})
}

// DeleteComment 删除自己的评论（软删）。
func DeleteComment(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	if err := service.Comments.Delete(c.Request.Context(), c.GetUint("user_id"), id); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "已删除"})
}

package utils

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// ApiResponse 定义通用的响应结构体
type ApiResponse struct {
	Code    int         `json:"code"`
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Error   string      `json:"error,omitempty"` // 错误信息，只有在失败时返回
	Data    interface{} `json:"data,omitempty"`  // 额外的成功数据
}

// SuccessResponse 成功响应
func SuccessResponse(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, ApiResponse{
		Code:    http.StatusOK,
		Status:  "success",
		Message: message,
		Data:    data,
	})
}

// SuccessResponseNoData 成功响应无数据
func SuccessResponseNoData(c *gin.Context, message string) {
	c.JSON(http.StatusOK, ApiResponse{
		Code:    http.StatusOK,
		Status:  "success",
		Message: message,
	})
}

// ErrorResponse 错误响应
func ErrorResponse(c *gin.Context, code int, error string, message string) {
	c.JSON(code, ApiResponse{
		Code:    code,
		Status:  "error",
		Message: message,
		Error:   error,
	})
}

// ErrorResponseNoError 错误响应无error
func ErrorResponseNoError(c *gin.Context, code int, message string) {
	c.JSON(code, ApiResponse{
		Code:    code,
		Status:  "error",
		Message: message,
	})
}

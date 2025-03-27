package middleware

import (
	"fmt"
	"github.com/TXM983/wallpaper-api/internal/logger"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// 定义复合键结构，包含IP和速率
type limiterKey struct {
	IP   string
	Rate int
}

// 存储限流器，键为limiterKey
var ipLimiters sync.Map

// 记录最后访问时间，键为limiterKey
var ipLastAccess sync.Map

// RateLimit 限流中间件
func RateLimit(perSecond int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if perSecond <= 0 {
			c.AbortWithStatusJSON(500, gin.H{"error": "invalid rate limit configuration"})
			return
		}

		key := limiterKey{IP: ip, Rate: perSecond}
		limiter := getLimiter(key, perSecond)

		if !limiter.Allow() {
			logger.LogInfo(fmt.Sprintf("Rate limit exceeded for IP: %s (Rate: %d/s)\n", ip, perSecond))
			c.AbortWithStatusJSON(429, gin.H{"error": "too many requests"})
			return
		}

		logger.LogInfo(fmt.Sprintf("Allowed access for IP: %s (Rate: %d/s)\n", ip, perSecond))

		// 更新最后访问时间（仅允许时更新）
		ipLastAccess.Store(key, time.Now())
		c.Next()
	}
}

// 获取或创建限流器
func getLimiter(key limiterKey, perSecond int) *rate.Limiter {
	limiter, exists := ipLimiters.Load(key)
	if !exists {
		// Burst建议至少为1，防止perSecond过小
		burst := perSecond * 2
		if burst < 1 {
			burst = 1
		}
		limiter = rate.NewLimiter(rate.Limit(perSecond), burst)
		ipLimiters.Store(key, limiter)
	}
	return limiter.(*rate.Limiter)
}

// InitRateLimiterCleanup 初始化清理任务
func InitRateLimiterCleanup() {
	go cleanupLimiters()
}

// 定期清理过期限流器
func cleanupLimiters() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		logger.LogInfo(fmt.Sprintf("Starting rate limiter cleanup..."))

		var expiredKeys []limiterKey
		ipLimiters.Range(func(key, value interface{}) bool {
			k := key.(limiterKey)
			if lastAccess, exists := ipLastAccess.Load(k); exists {
				if time.Since(lastAccess.(time.Time)) > 15*time.Minute {
					expiredKeys = append(expiredKeys, k)
				}
			} else {
				expiredKeys = append(expiredKeys, k)
			}
			return true
		})
		for _, k := range expiredKeys {
			ipLimiters.Delete(k)
			ipLastAccess.Delete(k)
			logger.LogInfo(fmt.Sprintf("Batch cleaned up limiter: %v\n", k))
		}
	}
}

package middleware

import (
	"fmt"
	"github.com/TXM983/wallpaper-api-v1/internal/logger"
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

// 记录限流起始时间，键为limiterKey
var ipBlockedUntil sync.Map

// RateLimit 限流中间件
func RateLimit(perSecond int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if perSecond <= 0 {
			c.AbortWithStatusJSON(500, gin.H{"error": "invalid rate limit configuration"})
			return
		}

		key := limiterKey{IP: ip, Rate: perSecond}

		// 确保时区正确加载
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			// 兜底方案：如果无法加载 "Asia/Shanghai"，使用固定时区
			loc = time.FixedZone("CST", 8*3600) // CST (China Standard Time) UTC+8
		}

		now := time.Now().In(loc) // 确保 `now` 具有时区信息

		// 检查封禁状态
		if blockedUntil, exists := ipBlockedUntil.Load(key); exists {
			blockTime := blockedUntil.(time.Time)
			if blockTime.IsZero() {
				blockTime = now
			} else {
				blockTime = blockTime.In(loc) // 确保带有时区
			}

			formattedTime := blockTime.Format("2006-01-02 15:04:05")

			if now.Before(blockTime) {
				logger.LogErrorAsync(fmt.Sprintf("IP %s is still blocked until %s", ip, formattedTime))
				c.AbortWithStatusJSON(429, gin.H{"error": "too many requests, please wait until " + formattedTime})
				return
			}

			ipBlockedUntil.Delete(key)
		}

		limiter := getLimiter(key, perSecond)

		if !limiter.Allow() {
			blockTime := now.Add(2 * time.Minute) // 设置封禁时间
			ipBlockedUntil.Store(key, blockTime)

			formattedTime := blockTime.Format("2006-01-02 15:04:05")
			logger.LogErrorAsync(fmt.Sprintf("Rate limit exceeded for IP: %s (Rate: %d/s), blocked until %s", ip, perSecond, formattedTime))
			c.AbortWithStatusJSON(429, gin.H{"error": "too many requests, please wait until " + formattedTime})
			return
		}

		logger.LogInfoAsync(fmt.Sprintf("Allowed access for IP: %s (Rate: %d/s)", ip, perSecond))
		ipLastAccess.Store(key, now) // 存入时确保 `now` 带时区
		c.Next()
	}
}

// 获取或创建限流器（并发安全）
func getLimiter(key limiterKey, perSecond int) *rate.Limiter {
	// 先尝试加载现有限流器
	if limiter, exists := ipLimiters.Load(key); exists {
		return limiter.(*rate.Limiter)
	}

	// 计算合理的突发值
	burst := perSecond
	if burst < 1 {
		burst = 1
	}

	// 创建新限流器（此时可能有其他goroutine也在创建）
	newLimiter := rate.NewLimiter(rate.Limit(perSecond), burst)

	// 原子性存储或获取已存在的实例
	limiter, loaded := ipLimiters.LoadOrStore(key, newLimiter)
	if loaded {
		// 其他goroutine已抢先存储，使用已存在的实例
		return limiter.(*rate.Limiter)
	}
	// 当前实例存储成功
	return newLimiter
}

// InitRateLimiterCleanup 初始化清理任务
func InitRateLimiterCleanup(interval time.Duration) {
	go cleanupLimiters(interval)
}

// 定期清理过期限流器
func cleanupLimiters(interval time.Duration) {
	ticker := time.NewTicker(interval)
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

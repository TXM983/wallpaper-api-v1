package middleware

import (
	"fmt"
	"github.com/TXM983/wallpaper-api-v1/internal/logger"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// 限流器存储，key为 "ip|rate"
var (
	ipLimiters     sync.Map // map[string]*rate.Limiter
	ipLastAccess   sync.Map // map[string]time.Time
	ipBlockedUntil sync.Map // map[string]time.Time
)

var (
	loc *time.Location
)

func init() {
	var err error
	loc, err = time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// 兜底方案
		loc = time.FixedZone("CST", 8*3600)
	}
}

// 构造统一 key 字符串
func makeKey(ip string, rate int) string {
	ip = strings.TrimSpace(ip)
	return fmt.Sprintf("%s|%d", ip, rate)
}

// RateLimit 限流中间件
func RateLimit(perSecond int, blockDuration time.Duration) gin.HandlerFunc {
	// 限制 burst 最大值
	const maxBurst = 10

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if perSecond <= 0 {
			c.AbortWithStatusJSON(400, gin.H{"error": "invalid rate limit configuration"})
			return
		}

		key := makeKey(ip, perSecond)
		now := time.Now().In(loc)

		// 检查封禁状态
		if blockTimeRaw, exists := ipBlockedUntil.Load(key); exists {
			blockTime := blockTimeRaw.(time.Time).In(loc)
			if now.Before(blockTime) {
				formatted := blockTime.Format("2006-01-02 15:04:05")
				logger.LogErrorAsync(fmt.Sprintf("IP %s is blocked until %s", ip, formatted))
				c.AbortWithStatusJSON(429, gin.H{"error": "too many requests, please wait until " + formatted})
				return
			}
			// 已过期，删除封禁记录
			ipBlockedUntil.Delete(key)
		}

		limiter := getLimiter(key, perSecond, maxBurst)
		if !limiter.Allow() {
			blockTime := now.Add(blockDuration)
			ipBlockedUntil.Store(key, blockTime)

			formatted := blockTime.Format("2006-01-02 15:04:05")
			logger.LogErrorAsync(fmt.Sprintf("Rate limit exceeded for IP: %s (Rate: %d/s), blocked until %s", ip, perSecond, formatted))
			c.AbortWithStatusJSON(429, gin.H{"error": "too many requests, please wait until " + formatted})
			return
		}

		// 限流通过，只更新最后访问时间，日志可选，这里去除频繁日志
		ipLastAccess.Store(key, now)
		c.Next()
	}
}

// getLimiter 获取或创建限流器
func getLimiter(key string, perSecond int, maxBurst int) *rate.Limiter {
	if limiterRaw, exists := ipLimiters.Load(key); exists {
		return limiterRaw.(*rate.Limiter)
	}

	burst := perSecond
	if burst < 1 {
		burst = 1
	}
	if burst > maxBurst {
		burst = maxBurst
	}

	newLimiter := rate.NewLimiter(rate.Limit(perSecond), burst)
	limiterRaw, loaded := ipLimiters.LoadOrStore(key, newLimiter)
	if loaded {
		return limiterRaw.(*rate.Limiter)
	}
	return newLimiter
}

// InitRateLimiterCleanup 初始化清理任务
func InitRateLimiterCleanup(cleanInterval time.Duration, expireDuration time.Duration) {
	go cleanupLimiters(cleanInterval, expireDuration)
}

// cleanupLimiters 定期清理过期的限流器和封禁信息
func cleanupLimiters(interval time.Duration, expireAfter time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		logger.LogInfo("Starting rate limiter cleanup...")

		var expiredKeys []string

		ipLimiters.Range(func(key, value interface{}) bool {
			k := key.(string)
			if lastAccessRaw, exists := ipLastAccess.Load(k); exists {
				lastAccess := lastAccessRaw.(time.Time)
				if time.Since(lastAccess) > expireAfter {
					expiredKeys = append(expiredKeys, k)
				}
			} else {
				// 没有访问记录也视为过期
				expiredKeys = append(expiredKeys, k)
			}
			return true
		})

		for _, k := range expiredKeys {
			ipLimiters.Delete(k)
			ipLastAccess.Delete(k)
			ipBlockedUntil.Delete(k)
			logger.LogInfo(fmt.Sprintf("Cleaned up limiter and block info for key: %s", k))
		}
	}
}

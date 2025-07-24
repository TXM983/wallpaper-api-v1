package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/TXM983/wallpaper-api-v1/internal/config"
	"github.com/TXM983/wallpaper-api-v1/internal/logger"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"
)

// StringSliceToInterfaceSlice 工具函数：将 []string 转换为 []interface{}
func StringSliceToInterfaceSlice(strs []string) []interface{} {
	result := make([]interface{}, len(strs))
	for i, v := range strs {
		result[i] = v
	}
	return result
}

// ValidateDeviceType 校验设备类型
func ValidateDeviceType(deviceType string) bool {
	return deviceType == "pc" || deviceType == "mobile"
}

var unlockScript = redis.NewScript(`
    if redis.call("GET", KEYS[1]) == ARGV[1] then
        return redis.call("DEL", KEYS[1])
    else
        return 0
    end
`)

func GetRandomWallpaper(rdb *redis.Client, deviceType string) (string, error) {
	ctx := context.Background()
	keyOriginal := "wallpaper:" + deviceType     // 原始壁纸列表
	keyCache := "wallpaper:cache:" + deviceType  // 缓存列表
	lockKey := "lock:wallpaper:" + deviceType    // Redis 分布式锁
	channel := "wallpaper_channel:" + deviceType // Pub/Sub 频道

	cacheExists, err := rdb.Exists(ctx, keyCache).Result()
	if err != nil {
		logger.LogErrorAsync(fmt.Sprintf("Error checking cache existence for key %s: %v", keyCache, err))
		return "", err
	}

	if cacheExists == 0 {
		lockValue := uuid.New().String()
		lockAcquired, err := rdb.SetNX(ctx, lockKey, lockValue, 5*time.Second).Result()
		if err != nil {
			logger.LogErrorAsync(fmt.Sprintf("Error acquiring lock %s: %v", lockKey, err))
			return "", err
		}

		if lockAcquired {
			defer unlockScript.Run(ctx, rdb, []string{lockKey}, lockValue)

			err = RefillCache(ctx, rdb, keyOriginal, keyCache)
			if err != nil {
				return "", err
			}
			rdb.Publish(ctx, channel, "done") // 通知其他请求缓存已填充
		} else {
			sub := rdb.Subscribe(ctx, channel)
			defer sub.Close()

			ctxTimeout, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()

			_, err := sub.ReceiveMessage(ctxTimeout)
			if err != nil {
				logger.LogErrorAsync(fmt.Sprintf("Error waiting for cache refill: %v", err))
				return "", err
			}
		}
	}

	selectedWallpaper, err := rdb.SPop(ctx, keyCache).Result()
	if errors.Is(err, redis.Nil) {
		logger.LogErrorAsync("Cache is empty, no wallpaper available.")
		return "", fmt.Errorf("no wallpapers available in cache")
	}
	if err != nil {
		logger.LogErrorAsync(fmt.Sprintf("Error fetching wallpaper from cache for device type %s: %v", deviceType, err))
		return "", err
	}

	return selectedWallpaper, nil
}

// RefillCache **重置缓存**
func RefillCache(ctx context.Context, rdb *redis.Client, keyOriginal, keyCache string) error {
	logger.LogInfo(fmt.Sprintf("Refilling cache for key %s from original key %s", keyCache, keyOriginal))
	wallpapers, err := rdb.SMembers(ctx, keyOriginal).Result()
	if err != nil {
		logger.LogErrorAsync(fmt.Sprintf("Error fetching original wallpapers for key %s: %v", keyOriginal, err))
		return err
	}
	if len(wallpapers) == 0 {
		logger.LogErrorAsync(fmt.Sprintf("No wallpapers available for device type %s", keyOriginal))
		return fmt.Errorf("no wallpapers available")
	}

	tx := rdb.TxPipeline()
	tx.Del(ctx, keyCache) // 清空旧缓存

	interfaceList := StringSliceToInterfaceSlice(wallpapers)
	tx.SAdd(ctx, keyCache, interfaceList...) // 添加到 Set 缓存中

	_, err = tx.Exec(ctx)
	if err != nil {
		logger.LogErrorAsync(fmt.Sprintf("Failed to refill cache for key %s: %v", keyCache, err))
		return fmt.Errorf("failed to refill cache: %v", err)
	}

	logger.LogInfoAsync(fmt.Sprintf("Successfully refilled cache for key %s", keyCache))
	return nil
}

// IsImageFile 检查文件是否是图片
func IsImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".bmp" || ext == ".webp"
}

// UploadToOSS 将图片上传到OSS并返回URL
func UploadToOSS(file *multipart.FileHeader, bucket *oss.Bucket, appConfig *config.AppConfig, deviceType string) (string, error) {
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %v", err)
	}
	defer src.Close()

	ossFilePath := fmt.Sprintf("%s/%s", deviceType, file.Filename)

	err = bucket.PutObject(ossFilePath, src)
	if err != nil {
		return "", fmt.Errorf("failed to upload file to OSS: %v", err)
	}

	ossFileURL := fmt.Sprintf("%s/%s", appConfig.CDN.BaseURL, ossFilePath)
	return ossFileURL, nil
}

// DeleteFromOSS 从OSS中删除指定文件
func DeleteFromOSS(fileName string, deviceType string, bucket *oss.Bucket) error {
	ossFilePath := fmt.Sprintf("%s/%s", deviceType, fileName)

	err := bucket.DeleteObject(ossFilePath)
	if err != nil {
		return fmt.Errorf("failed to delete file '%s' from OSS: %v", ossFilePath, err)
	}
	return nil
}

// AddToWallpaperCache 将图片添加到壁纸缓存中，检查是否存在，如果存在则先删除再添加
func AddToWallpaperCache(fileName string, rdb *redis.Client, deviceType string) error {
	err := rdb.SRem(context.Background(), "wallpaper:"+deviceType, fileName).Err()
	if err != nil {
		return fmt.Errorf("failed to remove image from wallpaper cache set: %v", err)
	}

	err = rdb.SAdd(context.Background(), "wallpaper:"+deviceType, fileName).Err()
	if err != nil {
		return fmt.Errorf("failed to add image to wallpaper cache set: %v", err)
	}

	return nil
}

// AddToRandomWallpaperCache 将图片添加到随机壁纸缓存中，检查是否存在，如果存在则先删除再添加
func AddToRandomWallpaperCache(fileName string, rdb *redis.Client, deviceType string) error {
	err := rdb.SRem(context.Background(), "wallpaper:cache:"+deviceType, fileName).Err()
	if err != nil {
		return fmt.Errorf("failed to remove image from random wallpaper cache set: %v", err)
	}

	err = rdb.SAdd(context.Background(), "wallpaper:cache:"+deviceType, fileName).Err()
	if err != nil {
		return fmt.Errorf("failed to add image to random wallpaper cache set: %v", err)
	}

	return nil
}

// RemoveFromWallpaperCache 从壁纸缓存中删除指定文件
func RemoveFromWallpaperCache(fileName string, rdb *redis.Client, deviceType string) error {
	err := rdb.SRem(context.Background(), "wallpaper:"+deviceType, fileName).Err()
	if err != nil {
		return fmt.Errorf("failed to remove image from wallpaper cache set: %v", err)
	}

	return nil
}

// RemoveFromRandomWallpaperCache 从随机壁纸缓存中删除指定文件
func RemoveFromRandomWallpaperCache(fileName string, rdb *redis.Client, deviceType string) error {
	err := rdb.SRem(context.Background(), "wallpaper:cache:"+deviceType, fileName).Err()
	if err != nil {
		return fmt.Errorf("failed to remove image from random wallpaper cache set: %v", err)
	}
	return nil
}

// GetWallpaperURLsFromOSS 获取指定 deviceType 下所有图片的 URL
func GetWallpaperURLsFromOSS(bucket *oss.Bucket, deviceType string, appConfig *config.AppConfig) ([]string, error) {

	prefix := deviceType + "/"
	marker := ""
	var fileURLs []string

	for {
		result, err := bucket.ListObjects(oss.Prefix(prefix), oss.Marker(marker), oss.MaxKeys(1000))
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %v", err)
		}

		for _, object := range result.Objects {
			if strings.HasSuffix(object.Key, ".alist") {
				continue // 跳过 .alist 文件
			}
			fileURL := fmt.Sprintf("%s/%s", appConfig.CDN.BaseURL, object.Key)
			fileURLs = append(fileURLs, fileURL)
		}

		if result.IsTruncated {
			marker = result.NextMarker
		} else {
			break
		}
	}

	return fileURLs, nil
}

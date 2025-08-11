package main

import (
	"context"
	"errors"
	"fmt"
	utils "github.com/TXM983/wallpaper-api-v1/internal/util"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TXM983/wallpaper-api-v1/internal/config"
	"github.com/TXM983/wallpaper-api-v1/internal/logger"
	"github.com/TXM983/wallpaper-api-v1/internal/middleware"
	"github.com/TXM983/wallpaper-api-v1/internal/service"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

var (
	rdb       *redis.Client
	appConfig *config.AppConfig
	ossClient *oss.Client
	bucket    *oss.Bucket
)

func main() {

	// 初始化日志
	logger.Init()

	// 加载配置
	appConfig = config.LoadConfig()

	// 初始化 Redis
	initRedis()

	// 初始化阿里云 OSS
	initOSS()

	// 30分钟执行一次清理任务，过期时间15分钟
	middleware.InitRateLimiterCleanup(30*time.Minute, 15*time.Minute)

	// **确保 Redis 和 OSS 初始化成功**
	if rdb == nil {
		panic("Redis initialization failed")
	}
	if bucket == nil {
		panic("OSS bucket initialization failed")
	}

	// **初始化壁纸缓存**
	err := resetCache(rdb, bucket)
	if err != nil {
		fmt.Printf("Failed to initialize wallpaper cache: %v", err)
		os.Exit(1)
	}

	// **创建 Gin 引擎**
	r := setupRouter()

	logger.LogInfo(fmt.Sprintf("Server started on port: %d", appConfig.Server.Port))

	// **启动 HTTP 服务器**
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", appConfig.Server.Port),
		Handler: r,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(fmt.Sprintf("Failed to start server: %v", err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 关闭 Redis 连接
	if err := rdb.Close(); err != nil {
		logger.LogInfo("Failed to close Redis: %v\n", err)
	}

	// **关闭 HTTP 服务器**
	if err := server.Close(); err != nil {
		panic(fmt.Sprintf("Server forced to shutdown: %v", err))
	}
}

func initRedis() {
	rdb = redis.NewClient(&redis.Options{
		Addr:         appConfig.Redis.Addr,
		Password:     appConfig.Redis.Password,
		DB:           appConfig.Redis.DB,
		PoolSize:     appConfig.Redis.PoolSize,
		MinIdleConns: appConfig.Redis.MinIdleConns,
	})

	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {

		logger.LogError("Failed to connect to Redis: %v\n", err)
		os.Exit(1) // 如果 Redis 连接失败，退出程序
	}

	logger.LogInfo("Connected to Redis successfully！")
	fmt.Println("Connected to Redis successfully！")
}

func initOSS() {
	var err error

	ossClient, err = oss.New(appConfig.OSS.Endpoint, appConfig.OSS.AccessKeyID, appConfig.OSS.AccessKeySecret)
	if err != nil {
		logger.LogError("Failed to connect to OSS: %v\n", err)
		os.Exit(1)
	}

	bucket, err = ossClient.Bucket(appConfig.OSS.Bucket)
	if err != nil {
		logger.LogError("Failed to get OSS bucket: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully initialized Alibaba Cloud OSS！")

	// 需要向阿里云OSS配置触发事件，上传或者删除事件将触发处理函数
}

func resetCache(rdb *redis.Client, bucket *oss.Bucket) error {
	ctx := context.Background()

	if err := rdb.Del(ctx, "wallpaper:pc", "wallpaper:mobile", "wallpaper:cache:pc", "wallpaper:cache:mobile").Err(); err != nil {
		return fmt.Errorf("failed to clear old cache: %v", err)
	}

	pcCount, err := populateWallpaperList(ctx, rdb, bucket, "pc/")
	if err != nil {
		return fmt.Errorf("failed to populate PC wallpapers: %v", err)
	}

	mobileCount, err := populateWallpaperList(ctx, rdb, bucket, "mobile/")
	if err != nil {
		return fmt.Errorf("failed to populate Mobile wallpapers: %v", err)
	}

	if err := initRandomWallpaperCache(rdb, "pc"); err != nil {
		return fmt.Errorf("error initializing random wallpaper cache for PC: %v", err)
	}
	if err := initRandomWallpaperCache(rdb, "mobile"); err != nil {
		return fmt.Errorf("error initializing random wallpaper cache for Mobile: %v", err)
	}

	logger.LogInfo("Wallpaper cache initialized successfully. PC count: %d, Mobile count: %d\n", pcCount, mobileCount)

	return nil
}

func refreshCacheByDevice(rdb *redis.Client, bucket *oss.Bucket, deviceType string) error {
	ctx := context.Background()

	if err := rdb.Del(ctx, "wallpaper:"+deviceType, "wallpaper:cache:"+deviceType).Err(); err != nil {
		return fmt.Errorf("failed to clear old cache: %v", err)
	}

	deviceTypeCount, err := populateWallpaperList(ctx, rdb, bucket, deviceType+"/")
	if err != nil {
		return fmt.Errorf("failed to populate %v wallpapers: %v", deviceType, err)
	}

	if err := initRandomWallpaperCache(rdb, deviceType); err != nil {
		return fmt.Errorf("error initializing random wallpaper cache for %v: %v", deviceType, err)
	}

	logger.LogInfo("Wallpaper cache initialized successfully. %v count: %d\n", deviceType, deviceTypeCount)

	return nil
}

func initRandomWallpaperCache(rdb *redis.Client, deviceType string) error {
	ctx := context.Background()
	keyOriginal := "wallpaper:" + deviceType    // 原始壁纸列表
	keyCache := "wallpaper:cache:" + deviceType // 缓存列表

	cacheExists, err := rdb.Exists(ctx, keyCache).Result()
	if err != nil {
		logger.LogError(fmt.Sprintf("Error checking cache existence for key %s: %v", keyCache, err))
		return err
	}

	if cacheExists == 0 {
		logger.LogInfo(fmt.Sprintf("Random wallpaper cache for %s is empty, refilling...", deviceType))
		err = service.RefillCache(ctx, rdb, keyOriginal, keyCache)
		if err != nil {
			logger.LogError(fmt.Sprintf("Error refilling random wallpaper cache for key %s: %v", keyCache, err))
			return err
		}
	}

	cacheLength, err := rdb.SCard(ctx, keyCache).Result() // 使用 SCard 获取 Set 长度
	if err != nil {
		logger.LogError(fmt.Sprintf("Error getting cache length for key %s: %v", keyCache, err))
		return err
	}
	fmt.Printf("Successfully refilled random wallpaper cache for %s. Cache length: %d\n", deviceType, cacheLength)

	return nil
}

// **从 OSS 读取文件并存入 Redis List**
func populateWallpaperList(ctx context.Context, rdb *redis.Client, bucket *oss.Bucket, prefix string) (int, error) {
	marker := ""
	var wallpaperList []string
	totalCount := 0

	for {
		objects, err := bucket.ListObjects(oss.Marker(marker), oss.Prefix(prefix), oss.MaxKeys(1000))
		if err != nil {
			return totalCount, fmt.Errorf("failed to list objects for %s: %v", prefix, err)
		}

		for _, object := range objects.Objects {
			if strings.HasSuffix(object.Key, ".alist") {
				continue // 跳过 .alist 文件
			}
			filename := getFilenameFromKey(object.Key)
			wallpaperList = append(wallpaperList, filename)
			totalCount++
		}

		if objects.IsTruncated {
			marker = objects.NextMarker
		} else {
			break
		}
	}

	if len(wallpaperList) > 0 {
		key := "wallpaper:" + strings.TrimSuffix(prefix, "/") // "wallpaper:pc" 或 "wallpaper:mobile"

		interfaceList := service.StringSliceToInterfaceSlice(wallpaperList)

		if err := rdb.SAdd(ctx, key, interfaceList...).Err(); err != nil {
			return totalCount, fmt.Errorf("failed to add wallpapers to Redis Set for %s: %v", prefix, err)
		}
	}

	return totalCount, nil
}

// 辅助函数，用于从对象Key中提取文件名
func getFilenameFromKey(objectKey string) string {
	parts := strings.Split(objectKey, "/")
	return parts[len(parts)-1]
}

func setupRouter() *gin.Engine {
	r := gin.New()

	r.Use(
		gin.Recovery(),
	)

	r.Static("/static", "./internal/static")

	files, err := filepath.Glob("internal/view/*.html")
	if err != nil {
		fmt.Println("Error loading HTML files:", err)
		return nil
	}

	r.LoadHTMLFiles(files...)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil) // 渲染 index.html 页面
	})

	// 新增 /resetCache 接口，并为其添加限流中间件
	r.GET("/resetCache", middleware.RateLimit(2, 5*time.Minute), func(c *gin.Context) {

		err := resetCache(rdb, bucket)
		if err != nil {
			utils.ErrorResponse(c, 500, err.Error(), "Failed to initialize cache")
			return
		}
		utils.SuccessResponseNoData(c, "Cache initialized successfully")
	})

	r.GET("/refreshCacheByDevice", middleware.RateLimit(2, 5*time.Minute), func(c *gin.Context) {
		deviceType := c.Query("type") // 获取查询参数 "type" 的值

		if !service.ValidateDeviceType(deviceType) {
			logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
			utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
			return
		}

		err := refreshCacheByDevice(rdb, bucket, deviceType)
		if err != nil {
			logger.LogError(fmt.Sprintf("Error refreshing cache for device type '%s': %v", deviceType, err))
			utils.ErrorResponse(c, 500, err.Error(), "Failed to refresh cache")
			return
		}

		logger.LogInfo(fmt.Sprintf("Cache for device type '%s' refreshed successfully", deviceType))
		utils.SuccessResponseNoData(c, fmt.Sprintf("Cache for device type '%s' refreshed successfully", deviceType))
	})

	// 给 /wallpaper 路由添加限流中间件 (群组)
	wallpaperGroup := r.Group("/wallpaper")
	{
		wallpaperGroup.Use(middleware.RateLimit(5, 5*time.Minute))
		wallpaperGroup.GET("", handleWallpaper)
	}

	r.NoRoute(func(c *gin.Context) {
		utils.ErrorResponseNoError(c, 404, "The page or route you requested does not exist")
	})

	// 图片上传接口
	r.POST("/upload", middleware.RateLimit(2, 24*time.Hour), uploadWallpapers)

	// 图片删除接口
	r.POST("/delete", middleware.RateLimit(2, 24*time.Hour), deleteWallpaper)

	// 查询指定deviceType下的所有图片
	r.GET("/selectImages", middleware.RateLimit(5, 5*time.Minute), getWallpapers)

	return r
}

func handleWallpaper(c *gin.Context) {
	// 获取请求参数
	deviceType := c.Query("type")
	dataType := c.Query("dataType") // 额外的参数，判断返回格式

	logger.LogInfoAsync("Received request for wallpaper, device type: %s, dataType: %s", deviceType, dataType)

	if !service.ValidateDeviceType(deviceType) {
		logger.LogErrorAsync(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
		return
	}

	filename, err := service.GetRandomWallpaper(rdb, deviceType)
	if err != nil {
		logger.LogErrorAsync(fmt.Sprintf("Error fetching wallpaper for device type %s: %v", deviceType, err))
		utils.ErrorResponse(c, 500, "server error", fmt.Sprintf("An error occurred while fetching the wallpaper for device type '%s'. Error: %v", deviceType, err))
		return
	}

	if filename == "" {
		logger.LogErrorAsync(fmt.Sprintf("No wallpaper found for device type %s", deviceType))
		utils.ErrorResponse(c, 404, "no wallpaper found", fmt.Sprintf("No wallpapers are available for the device type '%s'.", deviceType))
		return
	}

	imageURL := fmt.Sprintf("%s/%s/%s", appConfig.CDN.BaseURL, deviceType, filename)

	logger.LogInfoAsync(fmt.Sprintf("Returning wallpaper URL for device type %s: %s", deviceType, imageURL))

	switch dataType {
	case "json":
		utils.SuccessResponse(c, "Wallpaper URL retrieved successfully", imageURL)
		return
	case "url":
		c.String(http.StatusOK, "%s", imageURL)
		return
	}

	c.Redirect(http.StatusFound, imageURL)
}

// 上传图片接口
func uploadWallpapers(c *gin.Context) {

	deviceType := c.PostForm("type")   // 额外的参数，判断返回格式
	password := c.PostForm("password") // 额外的参数，判断返回格式

	if password != appConfig.INDEX.Password {
		utils.ErrorResponse(c, 400, "invalid password", "密码错误，请输入正确的密码")
		return
	}

	if !service.ValidateDeviceType(deviceType) {
		logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
		return
	}

	files := c.Request.MultipartForm.File["files"]
	if len(files) == 0 {
		utils.ErrorResponse(c, 400, "No files uploaded", "Please upload at least one image file.")
		return
	} else if len(files) > 5 {
		utils.ErrorResponse(c, 400, "Too many files uploaded", "You can upload a maximum of 5 images.")
		return
	}

	var uploadedFiles []string
	for _, file := range files {

		if !service.IsImageFile(file.Filename) {
			utils.ErrorResponse(c, 400, "Invalid file type", fmt.Sprintf("The file '%s' is not a valid image type.", file.Filename))
			return
		}

		ossFileURL, err := service.UploadToOSS(file, bucket, appConfig, deviceType)
		if err != nil {
			utils.ErrorResponse(c, 500, "Failed to upload image", fmt.Sprintf("Error uploading '%s' to OSS: %v", file.Filename, err))
			return
		}

		err = service.AddToWallpaperCache(file.Filename, rdb, deviceType)
		if err != nil {
			utils.ErrorResponse(c, 500, "Failed to update wallpaper cache", fmt.Sprintf("Error adding '%s' to wallpaper cache: %v", file.Filename, err))
			return
		}

		err = service.AddToRandomWallpaperCache(file.Filename, rdb, deviceType)
		if err != nil {
			utils.ErrorResponse(c, 500, "Failed to update random wallpaper cache", fmt.Sprintf("Error adding '%s' to random wallpaper cache: %v", file.Filename, err))
			return
		}

		uploadedFiles = append(uploadedFiles, ossFileURL)
	}

	utils.SuccessResponse(c, "Files uploaded successfully", uploadedFiles)
}

// 删除指定 deviceType 和 图片名称的壁纸接口
func deleteWallpaper(c *gin.Context) {
	type DeleteWallpaperRequest struct {
		DeviceType string `json:"type" binding:"required"`
		FileName   string `json:"fileName" binding:"required"`
		Password   string `json:"password" binding:"required"`
	}

	var req DeleteWallpaperRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.ErrorResponse(c, 400, "invalid parameters", "Invalid request parameters. Please check deviceType, fileName, and password.")
		return
	}

	if req.Password != appConfig.INDEX.Password {
		utils.ErrorResponse(c, 401, "invalid password", "Authentication failed. Incorrect password.")
		return
	}

	if !service.ValidateDeviceType(req.DeviceType) {
		logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", req.DeviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("Device type '%s' is not supported.", req.DeviceType))
		return
	}

	if err := service.DeleteFromOSS(req.FileName, req.DeviceType, bucket); err != nil {
		utils.ErrorResponse(c, 500, "delete error", fmt.Sprintf("Failed to delete '%s' from OSS: %v", req.FileName, err))
		return
	}

	if err := service.RemoveFromWallpaperCache(req.FileName, rdb, req.DeviceType); err != nil {
		utils.ErrorResponse(c, 500, "cache update error", fmt.Sprintf("Failed to remove '%s' from wallpaper cache: %v", req.FileName, err))
		return
	}

	if err := service.RemoveFromRandomWallpaperCache(req.FileName, rdb, req.DeviceType); err != nil {
		utils.ErrorResponse(c, 500, "random cache update error", fmt.Sprintf("Failed to remove '%s' from random wallpaper cache: %v", req.FileName, err))
		return
	}

	utils.SuccessResponse(c, "Image deleted successfully", nil)
}

// 查询壁纸的接口
func getWallpapers(c *gin.Context) {
	deviceType := c.Query("type") // 获取设备类型参数

	if !service.ValidateDeviceType(deviceType) {
		logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
		return
	}

	wallpaperURLs, err := service.GetWallpaperURLsFromOSS(bucket, deviceType, appConfig)
	if err != nil {
		utils.ErrorResponse(c, 500, "Failed to retrieve wallpapers", fmt.Sprintf("Error: %v", err))
		return
	}

	utils.SuccessResponse(c, "Wallpapers retrieved successfully", wallpaperURLs)
}

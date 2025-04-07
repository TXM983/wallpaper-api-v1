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

	// 启动后台清理任务
	middleware.InitRateLimiterCleanup(30 * time.Minute)

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

	// 测试 Redis 连接
	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {
		// 如果连接失败，打印错误并退出程序

		logger.LogError("Failed to connect to Redis: %v\n", err)
		fmt.Printf("Failed to connect to Redis: %v\n", err)
		os.Exit(1) // 如果 Redis 连接失败，退出程序
	}

	// 如果连接成功，打印 Redis 连接成功
	logger.LogInfo("Connected to Redis successfully！")
	fmt.Println("Connected to Redis successfully！")
}

func initOSS() {
	var err error
	// 创建OSS客户端
	ossClient, err = oss.New(appConfig.OSS.Endpoint, appConfig.OSS.AccessKeyID, appConfig.OSS.AccessKeySecret)
	if err != nil {
		logger.LogError("Failed to connect to OSS: %v\n", err)
		fmt.Printf("Failed to connect to OSS: %v\n", err)
		os.Exit(1)
	}

	// 获取OSS存储桶
	bucket, err = ossClient.Bucket(appConfig.OSS.Bucket)
	if err != nil {
		logger.LogError("Failed to get OSS bucket: %v\n", err)
		fmt.Printf("Failed to get OSS bucket: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully initialized Alibaba Cloud OSS！")

	// 需要向阿里云OSS配置触发事件，上传或者删除事件将触发处理函数
}

// **工具函数：将 []string 转换为 []interface{}**
func stringSliceToInterfaceSlice(strs []string) []interface{} {
	result := make([]interface{}, len(strs))
	for i, v := range strs {
		result[i] = v
	}
	return result
}

func resetCache(rdb *redis.Client, bucket *oss.Bucket) error {
	ctx := context.Background()

	// **清空 Redis 旧缓存**
	if err := rdb.Del(ctx, "wallpaper:pc", "wallpaper:mobile", "wallpaper:cache:pc", "wallpaper:cache:mobile").Err(); err != nil {
		return fmt.Errorf("failed to clear old cache: %v", err)
	}

	// **初始化 PC 和 Mobile 类型壁纸**
	pcCount, err := populateWallpaperList(ctx, rdb, bucket, "pc/")
	if err != nil {
		return fmt.Errorf("failed to populate PC wallpapers: %v", err)
	}

	mobileCount, err := populateWallpaperList(ctx, rdb, bucket, "mobile/")
	if err != nil {
		return fmt.Errorf("failed to populate Mobile wallpapers: %v", err)
	}

	// **初始化随机壁纸缓存**
	if err := initRandomWallpaperCache(rdb, "pc"); err != nil {
		return fmt.Errorf("error initializing random wallpaper cache for PC: %v", err)
	}
	if err := initRandomWallpaperCache(rdb, "mobile"); err != nil {
		return fmt.Errorf("error initializing random wallpaper cache for Mobile: %v", err)
	}

	// **打印最终的壁纸数量**
	logger.LogInfo("Wallpaper cache initialized successfully. PC count: %d, Mobile count: %d\n", pcCount, mobileCount)
	fmt.Printf("Wallpaper cache initialized successfully. PC count: %d, Mobile count: %d\n", pcCount, mobileCount)

	return nil
}

func refreshCacheByDevice(rdb *redis.Client, bucket *oss.Bucket, deviceType string) error {
	ctx := context.Background()

	// **根据deviceType清空 Redis 旧缓存**
	if err := rdb.Del(ctx, "wallpaper:"+deviceType, "wallpaper:cache:"+deviceType).Err(); err != nil {
		return fmt.Errorf("failed to clear old cache: %v", err)
	}

	// **根据deviceType初始化壁纸**
	deviceTypeCount, err := populateWallpaperList(ctx, rdb, bucket, deviceType+"/")
	if err != nil {
		return fmt.Errorf("failed to populate %v wallpapers: %v", deviceType, err)
	}

	// **根据deviceType初始化随机壁纸缓存**
	if err := initRandomWallpaperCache(rdb, deviceType); err != nil {
		return fmt.Errorf("error initializing random wallpaper cache for %v: %v", deviceType, err)
	}

	// **打印最终的壁纸缓存数量**
	logger.LogInfo("Wallpaper cache initialized successfully. %v count: %d\n", deviceType, deviceTypeCount)
	fmt.Printf("Wallpaper cache initialized successfully. %v count: %d\n", deviceType, deviceTypeCount)

	return nil
}

func initRandomWallpaperCache(rdb *redis.Client, deviceType string) error {
	ctx := context.Background()
	keyOriginal := "wallpaper:" + deviceType    // 原始壁纸列表
	keyCache := "wallpaper:cache:" + deviceType // 缓存列表

	// 检查缓存是否为空
	cacheExists, err := rdb.Exists(ctx, keyCache).Result()
	if err != nil {
		logger.LogError(fmt.Sprintf("Error checking cache existence for key %s: %v", keyCache, err))
		return err
	}

	// 如果缓存为空，则重新填充
	if cacheExists == 0 {
		logger.LogInfo(fmt.Sprintf("Random wallpaper cache for %s is empty, refilling...", deviceType))
		err = service.RefillCache(ctx, rdb, keyOriginal, keyCache)
		if err != nil {
			logger.LogError(fmt.Sprintf("Error refilling random wallpaper cache for key %s: %v", keyCache, err))
			return err
		}
	}

	cacheLength, err := rdb.LLen(ctx, keyCache).Result()
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
		// **每次最多获取 1000 个文件**
		objects, err := bucket.ListObjects(oss.Marker(marker), oss.Prefix(prefix), oss.MaxKeys(1000))
		if err != nil {
			return totalCount, fmt.Errorf("failed to list objects for %s: %v", prefix, err)
		}

		// **筛选有效文件**
		for _, object := range objects.Objects {
			if strings.HasSuffix(object.Key, ".alist") {
				continue // 跳过 .alist 文件
			}
			filename := getFilenameFromKey(object.Key)
			wallpaperList = append(wallpaperList, filename)
			totalCount++
		}

		// **检查是否还有更多文件**
		if objects.IsTruncated {
			marker = objects.NextMarker
		} else {
			break
		}
	}

	// **批量存入 Redis**
	if len(wallpaperList) > 0 {
		key := "wallpaper:" + strings.TrimSuffix(prefix, "/") // "wallpaper:pc" or "wallpaper:mobile"
		if err := rdb.LPush(ctx, key, stringSliceToInterfaceSlice(wallpaperList)...).Err(); err != nil {
			return totalCount, fmt.Errorf("failed to push wallpapers to Redis for %s: %v", prefix, err)
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

	// 中间件
	r.Use(
		gin.Recovery(),
	)

	r.Static("/static", "./internal/static")

	// 使用 Glob 获取目录下所有的 HTML 文件
	files, err := filepath.Glob("internal/view/*.html")
	if err != nil {
		fmt.Println("Error loading HTML files:", err)
		return nil
	}

	// 加载 HTML 文件
	r.LoadHTMLFiles(files...)

	// 路由
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 新增路由：提供 API 文档页面
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil) // 渲染 index.html 页面
	})

	// 新增 /resetCache 接口，并为其添加限流中间件
	r.GET("/resetCache", middleware.RateLimit(5), func(c *gin.Context) {

		// 调用 initWallpaperCache 函数，传入 redis 客户端和 OSS 存储桶
		err := resetCache(rdb, bucket)
		if err != nil {
			utils.ErrorResponse(c, 500, err.Error(), "Failed to initialize cache")
			return
		}
		utils.SuccessResponseNoData(c, "Cache initialized successfully")
	})

	// 新增 /refreshCacheByDevice 接口，并为其添加限流中间件
	r.GET("/refreshCacheByDevice", middleware.RateLimit(5), func(c *gin.Context) {
		deviceType := c.Query("type") // 获取查询参数 "type" 的值

		// 校验设备类型是否合法
		if !service.ValidateDeviceType(deviceType) {
			logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
			utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
			return
		}

		// 调用 refreshCacheByDevice 函数，传入 redis 客户端和 OSS 存储桶
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
		// 添加限流中间件（每秒 5 请求/每个 IP）
		wallpaperGroup.Use(middleware.RateLimit(5))
		wallpaperGroup.GET("", handleWallpaper)
	}

	// 处理路由不存在的情况
	r.NoRoute(func(c *gin.Context) {
		utils.ErrorResponseNoError(c, 404, "The page or route you requested does not exist")
	})

	// 图片上传接口
	r.POST("/upload", middleware.RateLimit(2), uploadWallpapers)

	// 图片删除接口
	r.GET("/delete", middleware.RateLimit(2), deleteWallpaper)

	// 查询指定deviceType下的所有图片
	r.GET("/selectImages", middleware.RateLimit(2), getWallpapers)

	return r
}

func handleWallpaper(c *gin.Context) {
	// 获取请求参数
	deviceType := c.Query("type")
	dataType := c.Query("dataType") // 额外的参数，判断返回格式

	// 记录接收到的请求信息
	logger.LogInfo("Received request for wallpaper, device type: %s, dataType: %s", deviceType, dataType)

	// 校验设备类型是否合法
	if !service.ValidateDeviceType(deviceType) {
		logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
		return
	}

	// 获取随机壁纸
	filename, err := service.GetRandomWallpaper(rdb, deviceType)
	if err != nil {
		logger.LogError(fmt.Sprintf("Error fetching wallpaper for device type %s: %v", deviceType, err))
		utils.ErrorResponse(c, 500, "server error", fmt.Sprintf("An error occurred while fetching the wallpaper for device type '%s'. Error: %v", deviceType, err))
		return
	}

	// 如果没有找到壁纸
	if filename == "" {
		logger.LogError(fmt.Sprintf("No wallpaper found for device type %s", deviceType))
		utils.ErrorResponse(c, 404, "no wallpaper found", fmt.Sprintf("No wallpapers are available for the device type '%s'.", deviceType))
		return
	}

	// 图片的绝对路径
	imageURL := fmt.Sprintf("%s/%s/%s", appConfig.CDN.BaseURL, deviceType, filename)

	// 记录返回的图片链接
	logger.LogInfo(fmt.Sprintf("Returning wallpaper URL for device type %s: %s", deviceType, imageURL))

	// 判断 dataType 是否为 "json" 或 "url"，决定返回 JSON 还是 302 跳转
	switch dataType {
	case "json":
		utils.SuccessResponse(c, "Wallpaper URL retrieved successfully", imageURL)
		return
	case "url":
		// 直接返回 URL，避免额外的 JSON 解析
		c.String(http.StatusOK, "%s", imageURL)
		return
	}

	// 默认 302 重定向
	c.Redirect(http.StatusFound, imageURL)
}

// 上传图片接口
func uploadWallpapers(c *gin.Context) {

	deviceType := c.PostForm("deviceType") // 额外的参数，判断返回格式
	password := c.PostForm("password")     // 上传图片时需要验证密码

	if password != appConfig.INDEX.Password {
		utils.ErrorResponse(c, 400, "invalid password", "密码错误，请输入正确的密码")
		return
	}

	// 校验设备类型是否合法
	if !service.ValidateDeviceType(deviceType) {
		logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
		return
	}

	// 获取上传的图片文件
	files := c.Request.MultipartForm.File["files"]
	if len(files) == 0 {
		utils.ErrorResponse(c, 400, "No files uploaded", "Please upload at least one image file.")
		return
	} else if len(files) > 5 {
		utils.ErrorResponse(c, 400, "Too many files uploaded", "You can upload a maximum of 5 images.")
		return
	}

	// 批量上传的结果
	var uploadedFiles []string
	for _, file := range files {
		// 校验文件类型是否是图片
		if !service.IsImageFile(file.Filename) {
			utils.ErrorResponse(c, 400, "Invalid file type", fmt.Sprintf("The file '%s' is not a valid image type.", file.Filename))
			return
		}

		// 上传文件到OSS
		ossFileURL, err := service.UploadToOSS(file, bucket, appConfig, deviceType)
		if err != nil {
			utils.ErrorResponse(c, 500, "Failed to upload image", fmt.Sprintf("Error uploading '%s' to OSS: %v", file.Filename, err))
			return
		}

		// 将图片添加到壁纸缓存
		err = service.AddToWallpaperCache(file.Filename, rdb, deviceType)
		if err != nil {
			utils.ErrorResponse(c, 500, "Failed to update wallpaper cache", fmt.Sprintf("Error adding '%s' to wallpaper cache: %v", file.Filename, err))
			return
		}

		// 将图片添加到随机壁纸缓存
		err = service.AddToRandomWallpaperCache(file.Filename, rdb, deviceType)
		if err != nil {
			utils.ErrorResponse(c, 500, "Failed to update random wallpaper cache", fmt.Sprintf("Error adding '%s' to random wallpaper cache: %v", file.Filename, err))
			return
		}

		uploadedFiles = append(uploadedFiles, ossFileURL)
	}

	// 返回上传成功的文件URL
	utils.SuccessResponse(c, "Files uploaded successfully", uploadedFiles)
}

// 删除指定 deviceType 和 图片名称的壁纸接口
func deleteWallpaper(c *gin.Context) {
	// Define request structure
	type DeleteWallpaperRequest struct {
		DeviceType string `json:"deviceType" binding:"required"`
		FileName   string `json:"fileName" binding:"required"`
		Password   string `json:"password" binding:"required"`
	}

	var req DeleteWallpaperRequest

	// Parse and validate request body
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.ErrorResponse(c, 400, "invalid parameters", "Invalid request parameters. Please check deviceType, fileName, and password.")
		return
	}

	// Password check (hash comparison recommended)
	if req.Password != appConfig.INDEX.Password {
		utils.ErrorResponse(c, 401, "invalid password", "Authentication failed. Incorrect password.")
		return
	}

	// Validate device type
	if !service.ValidateDeviceType(req.DeviceType) {
		logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", req.DeviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("Device type '%s' is not supported.", req.DeviceType))
		return
	}

	// Delete from OSS
	if err := service.DeleteFromOSS(req.FileName, req.DeviceType, bucket); err != nil {
		utils.ErrorResponse(c, 500, "delete error", fmt.Sprintf("Failed to delete '%s' from OSS: %v", req.FileName, err))
		return
	}

	// Remove from wallpaper cache
	if err := service.RemoveFromWallpaperCache(req.FileName, rdb, req.DeviceType); err != nil {
		utils.ErrorResponse(c, 500, "cache update error", fmt.Sprintf("Failed to remove '%s' from wallpaper cache: %v", req.FileName, err))
		return
	}

	// Remove from random wallpaper cache
	if err := service.RemoveFromRandomWallpaperCache(req.FileName, rdb, req.DeviceType); err != nil {
		utils.ErrorResponse(c, 500, "random cache update error", fmt.Sprintf("Failed to remove '%s' from random wallpaper cache: %v", req.FileName, err))
		return
	}

	// 返回删除成功的响应
	utils.SuccessResponse(c, "Image deleted successfully", nil)
}

// 查询壁纸的接口
func getWallpapers(c *gin.Context) {
	deviceType := c.Query("deviceType") // 获取设备类型参数

	// 校验设备类型是否合法
	if !service.ValidateDeviceType(deviceType) {
		logger.LogError(fmt.Sprintf("Invalid device type '%s' provided in request", deviceType))
		utils.ErrorResponse(c, 400, "invalid device type", fmt.Sprintf("The device type '%s' is not recognized or supported.", deviceType))
		return
	}

	// 获取图片 URL 列表
	wallpaperURLs, err := service.GetWallpaperURLsFromOSS(bucket, deviceType, appConfig)
	if err != nil {
		utils.ErrorResponse(c, 500, "Failed to retrieve wallpapers", fmt.Sprintf("Error: %v", err))
		return
	}

	// 返回图片 URL 列表
	utils.SuccessResponse(c, "Wallpapers retrieved successfully", wallpaperURLs)
}

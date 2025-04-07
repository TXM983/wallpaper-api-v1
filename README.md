<p align="center">
  <img src="https://neko.aimiliy.top/v1/favicon.ico" alt="Wallpaper-api-v1 Logo" width="100">
</p>

<h1 align="center">Wallpaper-api-v1</h1>

## 技术架构图

```
客户端 -> API -> Redis缓存 -> 阿里云OSS -> CDN 
      <- JSON响应 <-
```

## 核心实现步骤

文件结构：

```
wallpaper-api-v1/
├── .github/
│   └── workflows/
│       └── wallpaper-api-v1.yml
├── cmd/
│   ├── server/
│   │   └── main.go
├── configs/
│   └── config.yaml
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── logger/
│   │   └── logger.go
│   ├── middleware/
│   │   └── ratelimit.go
│   ├── service/
│   │   └── wallpaper.go
│   ├── static/
│   │   ├── fonts/
│   │   │   └── LWGX.woff2
│   │   └── favicon.ico
│   ├── util/
│   │   └── response.go
│   ├── view/
│   │   └── index.html
├── scripts/
```

## Redis缓存设计

```
# Key结构
wallpaper:pc = {wallpaper1.jpg, wallpaper2.jpg...}
wallpaper:mobile = {wp1.jpg, wp2.jpg...}
```

## docker部署

**新增docker-compose.yml配置文件，输入以下内容，`####`部分配置需要自行修改：**

```yaml
version: '3.8'

services:
  # Redis 服务
  redis:
    image: redis:alpine  # 使用官方的 Redis 镜像
    container_name: redis
    restart: always
    ports:
      - "6379:6379"  # 映射 Redis 的端口到宿主机
    volumes:
      - ./data/redis:/data  # 持久化 Redis 数据

  # wallpaper-api 服务
  wallpaper-api:
    image: txm123/wallpaper-api-v1:latest  # 你的 Docker Hub 镜像
    container_name: wallpaper-api
    restart: always
    ports:
      - "6523:6523"  # 映射宿主机端口 6523 到容器内部端口
    volumes:
      - ./logs:/app/logs  # 挂载日志文件目录
    environment:
      - GIN_MODE=release  # 设定 Gin 运行环境
      - SERVER_PORT=######## # 项目启动端口 同步修改容器内映射端口
      - REDIS_ADDR=redis:6379  # 使用 Redis 服务的容器名作为 Redis 地址
      - REDIS_PASSWORD=  # Redis 默认没有密码
      - REDIS_DB=0     # Redis 数据库的编号
      - REDIS_POOL_SIZE=100   # 连接池的最大连接数
      - REDIS_MIN_IDLE_CONNS=20  # 连接池中最小空闲连接数
      - CDN_BASE_URL=########   # oss cdn 访问地址
      - OSS_ENDPOINT=########    # OSS 区域 Endpoint
      - OSS_ACCESS_KEY_ID=########    # Access Key ID
      - OSS_ACCESS_KEY_SECRET=########    # Access Key Secret
      - OSS_BUCKET=########    # OSS 存储桶名称
      - LOG_FILE_PATH=#####  # 日志文件路径（非必填，需同步修改wallpaper-api挂载日志目录）
      - PASSWORD=###### # 上传删除接口密码
```

## 异步同步机制

### 使用阿里云OSS事件通知 + 函数计算 (暂未实现)

当OSS发生Put/Delete操作时：

1. 触发函数计算服务
2. 解析事件类型（上传/删除）
3. 更新对应Redis List集合


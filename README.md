# Wallpaper API

## 技术架构图

```
客户端 -> CDN -> API服务器 -> Redis缓存 -> 阿里云OSS
      <- JSON响应 <-
```

## 核心实现步骤

文件结构：

```
bucket/
├── pc/
│   ├── wallpaper1.jpg
│   └── wallpaper2.jpg
└── mobile/
    ├── wp1.jpg
    └── wp2.jpg
```

## Redis缓存设计

```
# Key结构
wallpaper:pc = {wallpaper1.jpg, wallpaper2.jpg...}
wallpaper:mobile = {wp1.jpg, wp2.jpg...}

# 使用Set数据结构实现O(1)复杂度随机获取
```

## 异步同步机制

### 使用阿里云OSS事件通知 + 函数计算

当OSS发生Put/Delete操作时：

1. 触发函数计算服务
2. 解析事件类型（上传/删除）
3. 更新对应Redis Set集合


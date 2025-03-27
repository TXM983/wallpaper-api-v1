#!/bin/sh

# 设置应用程序的名称
APP_NAME="wallpaper-api-v1"

# 检查进程是否存在
pid=$(pgrep -f $APP_NAME)

if [ -n "$pid" ]; then
    echo "Process $APP_NAME is running, killing it..."
    kill -9 $pid  # 杀掉进程
    sleep 2  # 等待进程完全退出
else
    echo "Process $APP_NAME is not running."
fi

# 启动新的应用程序
echo "Starting $APP_NAME..."
exec /app/$APP_NAME  # 启动新的进程

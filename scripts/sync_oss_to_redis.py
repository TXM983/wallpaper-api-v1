import os
import redis
from aliyunsdkcore.client import AcsClient
from aliyunsdkcore.acs_exception.exceptions import ClientException
from aliyunsdkcore.acs_exception.exceptions import ServerException

def handler(event, context):
    # 初始化客户端
    r = redis.Redis(
        host=os.getenv('REDIS_HOST'),
        port=6379,
        password=os.getenv('REDIS_PASSWORD'))

    # 解析OSS事件
    for record in event['events']:
        bucket = record['oss']['bucket']['name']
        object_key = record['oss']['object']['key']

        # 提取设备类型
        device_type = 'pc' if object_key.startswith('pc/') else 'mobile'
        filename = os.path.basename(object_key)

        # 更新Redis
        if record['eventName'].startswith('ObjectCreated:'):
            r.sadd(f'wallpaper:{device_type}', filename)
        elif record['eventName'].startswith('ObjectRemoved:'):
            r.srem(f'wallpaper:{device_type}', filename)

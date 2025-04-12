# 服务器月流量统计与警告系统

这是一个用于监控服务器流量并在达到指定阈值时发送警告的工具。该应用能够定期检查网络流量，并根据配置自动发送警告和月度流量报告。

## 功能

- 每月重置流量统计。
- 监控指定网络接口的流量（入站、出站、出入取大或双向）。
- 达到流量警告阈值时发送 Telegram 消息。
- 支持根据配置决定是否在达到阈值时关机。
- 支持定时发送月度流量报告。
- 配置灵活，可以通过 JSON 配置文件进行自定义设置。

## 配置文件

配置文件为 `config.json`，包括以下字段：

```json
{
  "monthly_reset_day": 1,
  "network_interface": "eth0",
  "traffic_mode": "both",
  "warning_threshold_gb": 1000,
  "check_interval_seconds": 300,
  "data_dir": "./data",
  "telegram_bot_token": "your_telegram_bot_token",
  "telegram_chat_ids": [123456789],
  "server_name": "MyServer",
  "shutdown_on_warning": false
}
```

### 字段说明

- `monthly_reset_day`: 每月流量重置的日期，默认值为 1（即每月的第一天）。
- `network_interface`: 要监控的网络接口名称，例如 `eth0` 或 `wlan0`。
- `traffic_mode`: 流量统计模式，可选值有：
  - `in`: 仅监控入站流量。
  - `out`: 仅监控出站流量。
  - `max`: 监控入站和出站流量的最大值。
  - `both`: 监控入站和出站流量的总和。
- `warning_threshold_gb`: 警告阈值，单位为 GB。
- `check_interval_seconds`: 检查流量的时间间隔，单位为秒。
- `data_dir`: 流量统计数据的存储目录。
- `telegram_bot_token`: 你的 Telegram Bot Token，用于发送警告和报告。
- `telegram_chat_ids`: 用于接收消息的 Telegram 聊天 ID 列表。
- `server_name`: 服务器名称，用于报告中标识。
- `shutdown_on_warning`: 达到流量警告阈值时是否关机（可选）。

## 使用教程

从[releases](https://github.com/Nei-Xin/traffic-monitor/releases)下载匹配您服务器 CPU 架构的最新二进制文件，并在当前文件夹下创建config.json文件，根据自己的需要，参考config_template.json，自定义config.json，解压并手动运行它。

```
./traffic-monitor 
```

#### 创建服务（可选）

如果您的系统使用 systemd，则可以创建一个服务来使中心在重新启动后继续运行。

1. 在 `/etc/systemd/system/traffic-monitor.service` 中创建一个服务文件。如果用户对工作目录具有写入权限，则可以使用非 root 用户。

```
[Unit]
Description=traffic-monitor
After=network.target

[Service]
ExecStart={/path/to/working/directory}/traffic-monitor
WorkingDirectory={/path/to/working/directory}
Restart=always
RestartSec=5
Type=simple

[Install]
WantedBy=multi-user.target
```

2. 启用并启动服务。

```
sudo systemctl daemon-reload
sudo systemctl enable beszel.service
sudo systemctl start beszel.service
```

## 示例输出

![image-20250412113552658](https://r2-img.neix.in/2025/04/12/20250412113555091.png)

## 最后

当前项目可能仍然存在一些未被发现的 bug 或者潜在的问题。请勿完全信任该项目，如有问题请及时反馈问题，我会继续改进和优化该项目。

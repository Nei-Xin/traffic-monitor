package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/net"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// 配置结构体
type Config struct {
	// 基本配置
	MonthlyResetDay      int      `json:"monthly_reset_day"`      // 每月更新日期
	NetworkInterface     string   `json:"network_interface"`      // 要监控的网络接口
	TrafficMode          string   `json:"traffic_mode"`           // 流量模式：in, out, max, both
	WarningThresholdGB   float64  `json:"warning_threshold_gb"`   // 警告阈值（GB）
	CheckIntervalSeconds int      `json:"check_interval_seconds"` // 检查间隔（秒）
	DataDir              string   `json:"data_dir"`               // 数据存储目录

	// Telegram机器人配置
	TelegramBotToken string   `json:"telegram_bot_token"` // Telegram Bot Token
	TelegramChatIDs  []int64  `json:"telegram_chat_ids"`  // Telegram聊天ID
	ServerName string `json:"server_name"` // 服务器名称
	ShutdownOnWarning    bool     `json:"shutdown_on_warning"` // 达到月流量阈值时是否关机
}

// 流量统计结构体
type TrafficStats struct {
	CurrentMonth          string    `json:"current_month"`
	LastResetTime         time.Time `json:"last_reset_time"`
	NextResetTime         time.Time `json:"next_reset_time"`
	BytesIn               uint64    `json:"bytes_in"`
	BytesOut              uint64    `json:"bytes_out"`
	LastBytesIn           uint64    `json:"last_bytes_in"`
	LastBytesOut          uint64    `json:"last_bytes_out"`
	WarningsSentThisMonth bool      `json:"warnings_sent_this_month"`
}

// 应用结构体
type App struct {
	Config      Config
	Stats       TrafficStats
	Bot         *tgbotapi.BotAPI
	mu          sync.Mutex
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

// 初始化应用
func NewApp(configPath string) (*App, error) {
	// 加载配置
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("无法加载配置: %v", err)
	}

	// 创建数据目录
	if config.DataDir == "" {
		config.DataDir = "./data"
	}
	err = os.MkdirAll(config.DataDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %v", err)
	}

	// 初始化Telegram Bot
	var bot *tgbotapi.BotAPI
	if config.TelegramBotToken != "" {
		bot, err = tgbotapi.NewBotAPI(config.TelegramBotToken)
		if err != nil {
			return nil, fmt.Errorf("初始化Telegram Bot失败: %v", err)
		}
		log.Printf("已授权账户 %s", bot.Self.UserName)
	}

	// 初始化应用
	app := &App{
		Config:   config,
		Bot:      bot,
		stopChan: make(chan struct{}),
	}

	// 加载或初始化统计数据
	err = app.loadOrInitStats()
	if err != nil {
		return nil, fmt.Errorf("加载统计数据失败: %v", err)
	}

	return app, nil
}

// 加载配置
func loadConfig(configPath string) (Config, error) {
	var config Config

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 如果配置文件不存在，创建默认配置
			config = Config{
				MonthlyResetDay:      1,
				NetworkInterface:     "eth0",
				TrafficMode:          "both",
				WarningThresholdGB:   1000, // 1000GB = 1TB
				CheckIntervalSeconds: 300,  // 5分钟
				DataDir:              "./data",
				TelegramBotToken:     "",
				TelegramChatIDs:      []int64{},
				ServerName:           "MyServer",
			}

			// 保存默认配置
			configData, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				return config, fmt.Errorf("创建默认配置失败: %v", err)
			}

			err = ioutil.WriteFile(configPath, configData, 0644)
			if err != nil {
				return config, fmt.Errorf("保存默认配置失败: %v", err)
			}

			log.Printf("已创建默认配置文件: %s", configPath)
			return config, nil
		}
		return config, err
	}

	err = json.Unmarshal(data, &config)
	if err != nil {
		return config, fmt.Errorf("解析配置文件失败: %v", err)
	}

	return config, nil
}

// 加载或初始化统计数据
func (app *App) loadOrInitStats() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	statsPath := filepath.Join(app.Config.DataDir, "traffic_stats.json")
	data, err := ioutil.ReadFile(statsPath)
	
	if err != nil {
		if os.IsNotExist(err) {
			// 如果统计数据不存在，初始化
			now := time.Now()
			nextResetTime := getNextResetTime(now, app.Config.MonthlyResetDay)
			
			// 获取当前流量基准
			bytesIn, bytesOut, err := getCurrentTrafficBytes(app.Config.NetworkInterface)
			if err != nil {
				return fmt.Errorf("获取当前流量失败: %v", err)
			}

			app.Stats = TrafficStats{
				CurrentMonth:          now.Format("2006-01"),
				LastResetTime:         now,
				NextResetTime:         nextResetTime,
				BytesIn:               0,
				BytesOut:              0,
				LastBytesIn:           bytesIn,
				LastBytesOut:          bytesOut,
				WarningsSentThisMonth: false,
			}

			// 保存初始化的统计数据
			return app.saveStats()
		}
		return err
	}

	// 解析统计数据
	err = json.Unmarshal(data, &app.Stats)
	if err != nil {
		return fmt.Errorf("解析统计数据失败: %v", err)
	}

	// 检查是否需要重置统计
	now := time.Now()
	if now.After(app.Stats.NextResetTime) {
		// 发送月度报告
		app.sendMonthlyReport()
		
		// 重置统计
		bytesIn, bytesOut, err := getCurrentTrafficBytes(app.Config.NetworkInterface)
		if err != nil {
			return fmt.Errorf("获取当前流量失败: %v", err)
		}

		app.Stats.CurrentMonth = now.Format("2006-01")
		app.Stats.LastResetTime = now
		app.Stats.NextResetTime = getNextResetTime(now, app.Config.MonthlyResetDay)
		app.Stats.BytesIn = 0
		app.Stats.BytesOut = 0
		app.Stats.LastBytesIn = bytesIn
		app.Stats.LastBytesOut = bytesOut
		app.Stats.WarningsSentThisMonth = false

		return app.saveStats()
	}

	return nil
}

// 保存统计数据
func (app *App) saveStats() error {
	statsPath := filepath.Join(app.Config.DataDir, "traffic_stats.json")
	
	data, err := json.MarshalIndent(app.Stats, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化统计数据失败: %v", err)
	}
	
	err = ioutil.WriteFile(statsPath, data, 0644)
	if err != nil {
		return fmt.Errorf("保存统计数据失败: %v", err)
	}
	
	return nil
}

// 获取下一个重置时间
func getNextResetTime(now time.Time, resetDay int) time.Time {
	year, month, _ := now.Date()
	
	// 确保重置日在1-28之间（避免月末问题）
	if resetDay < 1 {
		resetDay = 1
	} else if resetDay > 28 {
		resetDay = 28
	}
	
	// 计算下一个重置时间
	nextMonth := month + 1
	nextYear := year
	if nextMonth > 12 {
		nextMonth = 1
		nextYear++
	}
	
	return time.Date(nextYear, nextMonth, resetDay, 0, 0, 0, 0, now.Location())
}

// 获取当前网络接口的流量字节数
func getCurrentTrafficBytes(interfaceName string) (uint64, uint64, error) {
	stats, err := net.IOCounters(true)
	if err != nil {
		return 0, 0, err
	}
	
	for _, stat := range stats {
		if stat.Name == interfaceName {
			return stat.BytesRecv, stat.BytesSent, nil
		}
	}
	
	return 0, 0, fmt.Errorf("找不到网络接口: %s", interfaceName)
}

// 启动应用
func (app *App) Start() {
	log.Println("开始监控网络流量...")
	
	app.wg.Add(1)
	go app.monitorTraffic()
	
	// 设置信号处理以便优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	<-sigChan
	log.Println("正在关闭应用...")
	
	close(app.stopChan)
	app.wg.Wait()
	
	// 保存最终统计结果
	app.updateTrafficStats()
	
	log.Println("应用已关闭")
}

// 监控流量
func (app *App) monitorTraffic() {
	defer app.wg.Done()
	
	ticker := time.NewTicker(time.Duration(app.Config.CheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			err := app.updateTrafficStats()
			if err != nil {
				log.Printf("更新流量统计失败: %v", err)
			}
		case <-app.stopChan:
			return
		}
	}
}

// 更新流量统计
func (app *App) updateTrafficStats() error {
	app.mu.Lock()
	defer app.mu.Unlock()
	
	// 获取当前流量
	currentBytesIn, currentBytesOut, err := getCurrentTrafficBytes(app.Config.NetworkInterface)
	if err != nil {
		return err
	}
	
	// 计算增量
	inDelta := currentBytesIn - app.Stats.LastBytesIn
	outDelta := currentBytesOut - app.Stats.LastBytesOut
	
	// 更新累计统计
	app.Stats.BytesIn += inDelta
	app.Stats.BytesOut += outDelta
	app.Stats.LastBytesIn = currentBytesIn
	app.Stats.LastBytesOut = currentBytesOut
	
	// 检查是否需要发送警告
	if !app.Stats.WarningsSentThisMonth {
		var totalTraffic uint64
		
		switch app.Config.TrafficMode {
		case "in":
			totalTraffic = app.Stats.BytesIn
		case "out":
			totalTraffic = app.Stats.BytesOut
		case "max":
			if app.Stats.BytesIn > app.Stats.BytesOut {
				totalTraffic = app.Stats.BytesIn
			} else {
				totalTraffic = app.Stats.BytesOut
			}
		case "both":
			totalTraffic = app.Stats.BytesIn + app.Stats.BytesOut
		}
		
		// 将GB转换为字节进行比较
		warningThresholdBytes := uint64(app.Config.WarningThresholdGB * 1024 * 1024 * 1024)
		if totalTraffic >= warningThresholdBytes {
			app.sendWarningMessage(totalTraffic)
			app.Stats.WarningsSentThisMonth = true
		}
	}
	
	// 检查是否需要重置（以防定时器错过）
	now := time.Now()
	if now.After(app.Stats.NextResetTime) {
		// 发送月度报告
		app.sendMonthlyReport()
		
		// 重置统计
		app.Stats.CurrentMonth = now.Format("2006-01")
		app.Stats.LastResetTime = now
		app.Stats.NextResetTime = getNextResetTime(now, app.Config.MonthlyResetDay)
		app.Stats.BytesIn = 0
		app.Stats.BytesOut = 0
		app.Stats.WarningsSentThisMonth = false
	}
	
	// 保存统计
	return app.saveStats()
}

// 发送警告消息
func (app *App) sendWarningMessage(totalBytes uint64) {
	if app.Bot == nil {
		log.Println("警告：流量超过阈值，但未配置Telegram Bot")
		return
	}
	
	// 计算GB
	totalGB := float64(totalBytes) / 1024 / 1024 / 1024
	
        message := fmt.Sprintf("🚨 [%s] 流量警告: 本月已使用 %.2f GB，超过警告阈值 %.2f GB",
        		app.Config.ServerName, totalGB, app.Config.WarningThresholdGB)

	for _, chatID := range app.Config.TelegramChatIDs {
		msg := tgbotapi.NewMessage(chatID, message)
		_, err := app.Bot.Send(msg)
		if err != nil {
			log.Printf("发送警告消息失败 (chatID: %d): %v", chatID, err)
		}
	}
	
	log.Println("已发送流量警告消息")

	if app.Config.ShutdownOnWarning {
                log.Println("达到阈值，准备关机...")
                
		for _, chatID := range app.Config.TelegramChatIDs {
     		   msg := tgbotapi.NewMessage(chatID, message)
      		   _, err := app.Bot.Send(msg)
           	   if err != nil {
           	   log.Printf("发送关机警告消息失败 (chatID: %d): %v", chatID, err)
       		   }
    	        }
                
                go func() {
                        time.Sleep(10 * time.Second) // 留时间发送完消息
                        syscall.Sync()
                        syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
                }()
        }
}

// 发送月度报告
func (app *App) sendMonthlyReport() {
	if app.Bot == nil {
		log.Println("月度报告：未配置Telegram Bot，跳过发送")
		return
	}
	
	var totalTraffic uint64
	
	switch app.Config.TrafficMode {
	case "in":
		totalTraffic = app.Stats.BytesIn
	case "out":
		totalTraffic = app.Stats.BytesOut
	case "max":
		if app.Stats.BytesIn > app.Stats.BytesOut {
			totalTraffic = app.Stats.BytesIn
		} else {
			totalTraffic = app.Stats.BytesOut
		}
	case "both":
		totalTraffic = app.Stats.BytesIn + app.Stats.BytesOut
	}
	
	inGB := float64(app.Stats.BytesIn) / 1024 / 1024 / 1024
	outGB := float64(app.Stats.BytesOut) / 1024 / 1024 / 1024
	totalGB := float64(totalTraffic) / 1024 / 1024 / 1024
	
	message := fmt.Sprintf("📊 [%s] 月度流量报告 (%s)\n\n"+
        "- 入站流量: %.2f GB\n"+
        "- 出站流量: %.2f GB\n"+
        "- 总计流量: %.2f GB\n\n"+
        "流量统计模式: %s\n"+
        "下次重置时间: %s",
        app.Config.ServerName,
        app.Stats.CurrentMonth,
        inGB,
        outGB,
        totalGB,
        getTrafficModeDescription(app.Config.TrafficMode),
        app.Stats.NextResetTime.Format("2006-01-02"))


	for _, chatID := range app.Config.TelegramChatIDs {
		msg := tgbotapi.NewMessage(chatID, message)
		_, err := app.Bot.Send(msg)
		if err != nil {
			log.Printf("发送月度报告失败 (chatID: %d): %v", chatID, err)
		}
	}
	
	log.Println("已发送月度流量报告")
}

// 获取流量模式描述
func getTrafficModeDescription(mode string) string {
	switch mode {
	case "in":
		return "仅入站流量"
	case "out":
		return "仅出站流量"
	case "max":
		return "出入取大值"
	case "both":
		return "双向流量总和"
	default:
		return "未知模式"
	}
}

// 主函数
func main() {
	log.Println("服务器月流量统计启动中...")
	
	configPath := "config.json"
	
	// 检查命令行参数
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}
	
	app, err := NewApp(configPath)
	if err != nil {
		log.Fatalf("初始化应用失败: %v", err)
	}
	
	app.Start()
}

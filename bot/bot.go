package bot

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"oci-bot/config"
	"oci-bot/ippure"
	"oci-bot/oci"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// IPPurityCache stores purity info for checked IPs
type IPPurityCache struct {
	PurityScore string
	IPType      string
	IsNative    string
}

// AutoApplyConfig stores auto-apply task settings
type AutoApplyConfig struct {
	AccountName     string             // Selected account
	PurityThreshold int                // Max purity score threshold (e.g., 50 means <= 50%)
	NativeRequired  string             // "原生IP" / "非原生IP" / "any"
	MatchMode       string             // "all" (both conditions) / "any" (one condition)
	IntervalMin     int                // Min interval seconds
	IntervalMax     int                // Max interval seconds
	Active          bool               // Is auto-apply running
	Cancel          context.CancelFunc // To stop the task
	ChatID          int64              // Chat ID to send notifications
}

// AutoApplyWizard tracks the wizard setup state
type AutoApplyWizard struct {
	Step            int // Current step: 1=account, 2=purity, 3=native, 4=mode, 5=interval
	AccountName     string
	PurityThreshold int
	NativeRequired  string
	MatchMode       string
	ChatID          int64
}

// Bot represents the Telegram bot
type Bot struct {
	api           *tgbotapi.BotAPI
	cfg           *config.Config
	clients       map[string]*oci.Client
	currentClient *oci.Client
	adminID       int64
	mu            sync.Mutex
	purityCache   map[string]*IPPurityCache // IP -> purity info cache
	autoApply     *AutoApplyConfig          // Auto-apply task config
	autoWizard    *AutoApplyWizard          // Auto-apply wizard state
}

// New creates a new Telegram bot
func New(cfg *config.Config) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Telegram bot: %w", err)
	}

	log.Printf("Telegram bot authorized: @%s", api.Self.UserName)

	clients := make(map[string]*oci.Client)
	var firstClient *oci.Client
	for _, acc := range cfg.Accounts {
		client, err := oci.NewClient(&acc)
		if err != nil {
			log.Printf("Warning: failed to create OCI client for [%s]: %v", acc.Name, err)
			continue
		}
		clients[acc.Name] = client
		if firstClient == nil {
			firstClient = client
		}
		log.Printf("Loaded OCI account: [%s] (%s)", acc.Name, acc.Region)
	}

	if len(clients) == 0 {
		return nil, fmt.Errorf("no valid OCI accounts configured")
	}

	// Set bot commands menu
	commands := []tgbotapi.BotCommand{
		{Command: "accounts", Description: "列出所有账号"},
		{Command: "use", Description: "切换账号"},
		{Command: "newip", Description: "创建预留IP"},
		{Command: "listip", Description: "列出IP"},
		{Command: "delip", Description: "删除IP"},
		{Command: "checkip", Description: "检测IP纯净度"},
		{Command: "autoip", Description: "自动刷IP"},
		{Command: "stopauto", Description: "停止自动刷IP"},
		{Command: "help", Description: "帮助"},
	}
	cmdConfig := tgbotapi.NewSetMyCommands(commands...)
	api.Send(cmdConfig)
	log.Printf("Bot commands menu configured")

	return &Bot{
		api:           api,
		cfg:           cfg,
		clients:       clients,
		currentClient: firstClient,
		adminID:       cfg.TelegramAdminID,
		purityCache:   make(map[string]*IPPurityCache),
	}, nil
}

// Run starts the bot and listens for updates
func (b *Bot) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	log.Println("Bot is running, waiting for commands...")

	for {
		select {
		case <-ctx.Done():
			log.Println("Bot stopped")
			return nil
		case update := <-updates:
			if update.CallbackQuery != nil {
				b.handleCallback(update.CallbackQuery)
				continue
			}
			if update.Message == nil {
				continue
			}
			b.handleMessage(update.Message)
		}
	}
}

// handleCallback handles inline button clicks
func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	if cb.From.ID != b.adminID {
		return
	}

	data := cb.Data
	log.Printf("Callback: %s", data)

	// Answer callback to remove loading state
	callback := tgbotapi.NewCallback(cb.ID, "")
	b.api.Request(callback)

	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return
	}

	action := parts[0]
	param := parts[1]

	switch action {
	case "use":
		b.switchAccount(cb.Message.Chat.ID, param)
	case "del":
		b.deleteIP(cb.Message.Chat.ID, param)
	case "newip":
		b.createIP(cb.Message.Chat.ID)
	case "refresh":
		b.showIPList(cb.Message.Chat.ID)
	case "check":
		b.checkIPFromCallback(cb.Message.Chat.ID, param)
	case "autoip":
		b.handleAutoIPCallback(cb.Message.Chat.ID, param, parts)
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	log.Printf("Message from %d: %s", msg.From.ID, msg.Text)

	if msg.From.ID != b.adminID {
		b.reply(msg.Chat.ID, fmt.Sprintf("⛔ Unauthorized\nYour ID: %d", msg.From.ID))
		return
	}

	// Check if we're waiting for interval input in auto-apply wizard
	if !msg.IsCommand() {
		b.mu.Lock()
		wizard := b.autoWizard
		b.mu.Unlock()

		if wizard != nil && wizard.Step == 5 {
			// Expecting interval input
			b.handleIntervalInput(msg.Chat.ID, msg.Text)
			return
		}

		b.reply(msg.Chat.ID, "Use /help")
		return
	}

	cmd := msg.Command()
	args := msg.CommandArguments()

	switch cmd {
	case "start", "help":
		b.handleHelp(msg.Chat.ID)
	case "accounts":
		b.showAccounts(msg.Chat.ID)
	case "use":
		if args != "" {
			b.switchAccount(msg.Chat.ID, args)
		} else {
			b.showAccounts(msg.Chat.ID)
		}
	case "newip":
		b.createIP(msg.Chat.ID)
	case "listip":
		b.showIPList(msg.Chat.ID)
	case "delip":
		if args != "" {
			b.deleteIP(msg.Chat.ID, args)
		} else {
			b.showIPList(msg.Chat.ID)
		}
	case "checkip":
		if args != "" {
			b.checkIP(msg.Chat.ID, args)
		} else {
			b.reply(msg.Chat.ID, "用法: /checkip <IP地址>\n例如: /checkip 8.8.8.8")
		}
	case "autoip":
		b.startAutoIPWizard(msg.Chat.ID)
	case "stopauto":
		b.stopAutoApply(msg.Chat.ID)
	case "id":
		b.reply(msg.Chat.ID, fmt.Sprintf("Your ID: %d", msg.From.ID))
	default:
		b.reply(msg.Chat.ID, "Unknown command. /help")
	}
}

func (b *Bot) handleHelp(chatID int64) {
	help := fmt.Sprintf(`🤖 *OCI IP Bot*

/accounts - 选择账号
/newip - 创建预留IP
/listip - 列出IP
/checkip <IP> - 检测IP纯净度
/autoip - 自动刷IP
/stopauto - 停止自动刷IP

📍 *当前:* [%s] %s`, b.currentClient.AccountName(), b.currentClient.Region())

	b.replyMarkdown(chatID, help)
}

// showAccounts shows account list with clickable buttons
func (b *Bot) showAccounts(chatID int64) {
	var buttons [][]tgbotapi.InlineKeyboardButton

	for name, client := range b.clients {
		label := fmt.Sprintf("%s (%s)", name, client.Region())
		if client == b.currentClient {
			label = "✅ " + label
		}
		btn := tgbotapi.NewInlineKeyboardButtonData(label, "use:"+name)
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{btn})
	}

	msg := tgbotapi.NewMessage(chatID, "� *选择账号*")
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

// switchAccount switches to the specified account and shows IP list
func (b *Bot) switchAccount(chatID int64, name string) {
	client, ok := b.clients[name]
	if !ok {
		b.reply(chatID, "❌ 账号不存在: "+name)
		return
	}

	b.mu.Lock()
	b.currentClient = client
	b.mu.Unlock()

	// Show IP list after switching
	b.showIPList(chatID)
}

// showIPList shows IP list with query and delete buttons for each IP
func (b *Bot) showIPList(chatID int64) {
	b.showIPListWithHighlight(chatID, "", nil)
}

// showIPListWithHighlight shows IP list with optional highlight for a newly created IP
// highlightIP: the IP address to mark as new (empty string means no highlight)
// useClient: optional client to use (nil means use currentClient)
func (b *Bot) showIPListWithHighlight(chatID int64, highlightIP string, useClient *oci.Client) {
	b.mu.Lock()
	client := useClient
	if client == nil {
		client = b.currentClient
	}
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ips, err := client.ListReservedIPs(ctx)
	if err != nil {
		b.reply(chatID, "❌ "+err.Error())
		return
	}

	header := fmt.Sprintf("📋 *[%s]*\n%s\n\n", client.AccountName(), client.Region())

	if len(ips) == 0 {
		// No IPs - show create button only
		btn := tgbotapi.NewInlineKeyboardButtonData("➕ 申请IP", "newip:1")
		keyboard := tgbotapi.NewInlineKeyboardMarkup([]tgbotapi.InlineKeyboardButton{btn})

		msg := tgbotapi.NewMessage(chatID, header+"暂无预留IP")
		msg.ParseMode = tgbotapi.ModeMarkdown
		msg.ReplyMarkup = keyboard
		b.api.Send(msg)
		return
	}

	var sb strings.Builder
	sb.WriteString(header)

	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, ip := range ips {
		// Check if we have cached purity info for this IP
		b.mu.Lock()
		cache, hasPurity := b.purityCache[ip.IPAddress]
		b.mu.Unlock()

		// Check if this is the highlighted (newly created) IP
		isNew := highlightIP != "" && ip.IPAddress == highlightIP

		if hasPurity {
			// Show IP with purity info (score/type/source)
			if isNew {
				sb.WriteString(fmt.Sprintf("🆕 `%s` (%s/%s/%s)\n", ip.IPAddress, cache.PurityScore, cache.IPType, cache.IsNative))
			} else {
				sb.WriteString(fmt.Sprintf("• `%s` (%s/%s/%s)\n", ip.IPAddress, cache.PurityScore, cache.IPType, cache.IsNative))
			}
		} else {
			// Show IP without purity info
			if isNew {
				sb.WriteString(fmt.Sprintf("🆕 `%s`\n", ip.IPAddress))
			} else {
				sb.WriteString(fmt.Sprintf("• `%s`\n", ip.IPAddress))
			}
		}

		// Create query and delete buttons for each IP
		checkBtn := tgbotapi.NewInlineKeyboardButtonData("🔍 查询", "check:"+ip.IPAddress)
		delBtn := tgbotapi.NewInlineKeyboardButtonData("🗑 删除", "del:"+ip.IPAddress)
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{checkBtn, delBtn})
	}

	// Add create and refresh buttons at the bottom
	createBtn := tgbotapi.NewInlineKeyboardButtonData("➕ 申请IP", "newip:1")
	refreshBtn := tgbotapi.NewInlineKeyboardButtonData("🔄 刷新", "refresh:1")
	buttons = append(buttons, []tgbotapi.InlineKeyboardButton{createBtn, refreshBtn})

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

// createIP creates a new reserved IP
func (b *Bot) createIP(chatID int64) {
	b.mu.Lock()
	client := b.currentClient
	b.mu.Unlock()

	b.reply(chatID, fmt.Sprintf("⏳ [%s] 正在创建...", client.AccountName()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	displayName := fmt.Sprintf("tg-%d", time.Now().Unix())
	publicIP, err := client.CreateReservedIP(ctx, displayName)
	if err != nil {
		b.reply(chatID, "❌ "+err.Error())
		return
	}

	publicIP, err = client.WaitForIPReady(ctx, publicIP.ID, 60*time.Second)
	if err != nil {
		b.reply(chatID, "❌ "+err.Error())
		return
	}

	// Check if auto-check is enabled
	if b.cfg.AutoCheckIP {
		b.reply(chatID, fmt.Sprintf("✅ IP 创建成功: `%s`\n🔍 正在检测纯净度...", publicIP.IPAddress))

		checkCtx, checkCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer checkCancel()

		info, err := ippure.Check(checkCtx, publicIP.IPAddress)
		if err != nil {
			text := fmt.Sprintf("✅ *创建成功*\n\nIP: `%s`\n\n⚠️ 纯净度检测失败: %s\n\n📍 [%s] %s",
				publicIP.IPAddress, err.Error(), client.AccountName(), client.Region())
			checkBtn := tgbotapi.NewInlineKeyboardButtonURL("🔍 手动检测", "https://ippure.com/?ip="+publicIP.IPAddress)
			refreshBtn := tgbotapi.NewInlineKeyboardButtonData("📋 查看列表", "refresh:1")
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				[]tgbotapi.InlineKeyboardButton{checkBtn},
				[]tgbotapi.InlineKeyboardButton{refreshBtn},
			)
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = tgbotapi.ModeMarkdown
			msg.ReplyMarkup = keyboard
			b.api.Send(msg)
			return
		}

		// Cache the purity info
		b.mu.Lock()
		b.purityCache[publicIP.IPAddress] = &IPPurityCache{
			PurityScore: info.PurityScore,
			IPType:      info.IPType,
			IsNative:    info.IsNative,
		}
		b.mu.Unlock()

		text := fmt.Sprintf(`✅ *创建成功*

IP: `+"`%s`"+`

📊 *纯净度:* %s (%s)
🏢 *类型:* %s
🌐 *来源:* %s

📍 [%s] %s`,
			publicIP.IPAddress,
			info.PurityScore, info.PurityLevel,
			info.IPType,
			info.IsNative,
			client.AccountName(), client.Region())

		refreshBtn := tgbotapi.NewInlineKeyboardButtonData("📋 查看列表", "refresh:1")
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			[]tgbotapi.InlineKeyboardButton{refreshBtn},
		)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ParseMode = tgbotapi.ModeMarkdown
		msg.ReplyMarkup = keyboard
		b.api.Send(msg)
		return
	}

	// Show success with check link button (auto-check disabled)
	text := fmt.Sprintf("✅ *创建成功*\n\nIP: `%s`\n\n📍 [%s] %s",
		publicIP.IPAddress, client.AccountName(), client.Region())

	checkBtn := tgbotapi.NewInlineKeyboardButtonURL("🔍 检测原生IP", "https://ippure.com/?ip="+publicIP.IPAddress)
	refreshBtn := tgbotapi.NewInlineKeyboardButtonData("📋 查看列表", "refresh:1")
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{checkBtn},
		[]tgbotapi.InlineKeyboardButton{refreshBtn},
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// deleteIP deletes the specified IP
func (b *Bot) deleteIP(chatID int64, ipAddr string) {
	b.mu.Lock()
	client := b.currentClient
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ips, err := client.ListReservedIPs(ctx)
	if err != nil {
		b.reply(chatID, "❌ "+err.Error())
		return
	}

	var targetID string
	for _, ip := range ips {
		if ip.IPAddress == ipAddr {
			targetID = ip.ID
			break
		}
	}

	if targetID == "" {
		b.reply(chatID, "❌ 未找到: "+ipAddr)
		return
	}

	err = client.DeleteReservedIP(ctx, targetID)
	if err != nil {
		b.reply(chatID, "❌ "+err.Error())
		return
	}

	b.reply(chatID, "✅ 已删除: "+ipAddr)

	// Refresh IP list
	b.showIPList(chatID)
}

// checkIP checks the purity of an IP address
func (b *Bot) checkIP(chatID int64, ipAddr string) {
	// Validate IP address
	if net.ParseIP(ipAddr) == nil {
		b.reply(chatID, "❌ 无效的IP地址: "+ipAddr)
		return
	}

	b.reply(chatID, fmt.Sprintf("🔍 正在检测 %s ...", ipAddr))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := ippure.Check(ctx, ipAddr)
	if err != nil {
		b.reply(chatID, "❌ 检测失败: "+err.Error())
		return
	}

	// Cache the purity info
	b.mu.Lock()
	b.purityCache[ipAddr] = &IPPurityCache{
		PurityScore: info.PurityScore,
		IPType:      info.IPType,
		IsNative:    info.IsNative,
	}
	b.mu.Unlock()

	text := fmt.Sprintf(`🔍 *IP 纯净度检测*

IP: `+"`%s`"+`

📊 *纯净度:* %s (%s)
🏢 *类型:* %s
🌐 *来源:* %s`,
		info.IPAddress,
		info.PurityScore, info.PurityLevel,
		info.IPType,
		info.IsNative)

	b.replyMarkdown(chatID, text)
}

// checkIPFromCallback checks IP purity from callback button, caches result, and refreshes list
func (b *Bot) checkIPFromCallback(chatID int64, ipAddr string) {
	// Validate IP address
	if net.ParseIP(ipAddr) == nil {
		b.reply(chatID, "❌ 无效的IP地址: "+ipAddr)
		return
	}

	b.reply(chatID, fmt.Sprintf("🔍 正在检测 %s ...", ipAddr))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := ippure.Check(ctx, ipAddr)
	if err != nil {
		b.reply(chatID, "❌ 检测失败: "+err.Error())
		return
	}

	// Cache the purity info
	b.mu.Lock()
	b.purityCache[ipAddr] = &IPPurityCache{
		PurityScore: info.PurityScore,
		IPType:      info.IPType,
		IsNative:    info.IsNative,
	}
	b.mu.Unlock()

	// Show detection result
	text := fmt.Sprintf(`✅ *检测完成*

IP: `+"`%s`"+`

📊 *纯净度:* %s (%s)
🏢 *类型:* %s
🌐 *来源:* %s`,
		info.IPAddress,
		info.PurityScore, info.PurityLevel,
		info.IPType,
		info.IsNative)

	b.replyMarkdown(chatID, text)

	// Refresh the IP list to show updated purity info
	b.showIPList(chatID)
}

func (b *Bot) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	b.api.Send(msg)
}

func (b *Bot) replyMarkdown(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true
	b.api.Send(msg)
}

// ========== Auto-Apply IP Wizard ==========

// startAutoIPWizard starts the auto-apply IP configuration wizard
func (b *Bot) startAutoIPWizard(chatID int64) {
	// Check if auto-apply is already running
	b.mu.Lock()
	if b.autoApply != nil && b.autoApply.Active {
		b.mu.Unlock()
		b.reply(chatID, "⚠️ 自动刷IP任务正在运行中\n使用 /stopauto 停止当前任务")
		return
	}

	// Initialize wizard
	b.autoWizard = &AutoApplyWizard{
		Step:   1,
		ChatID: chatID,
	}
	b.mu.Unlock()

	// Step 1: Show account selection
	var buttons [][]tgbotapi.InlineKeyboardButton
	for name, client := range b.clients {
		label := fmt.Sprintf("%s (%s)", name, client.Region())
		btn := tgbotapi.NewInlineKeyboardButtonData(label, "autoip:account:"+name)
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{btn})
	}
	cancelBtn := tgbotapi.NewInlineKeyboardButtonData("❌ 取消", "autoip:cancel:")
	buttons = append(buttons, []tgbotapi.InlineKeyboardButton{cancelBtn})

	msg := tgbotapi.NewMessage(chatID, "🔄 *自动刷IP配置* (1/5)\n\n请选择账号:")
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

// handleAutoIPCallback handles auto-apply wizard callbacks
func (b *Bot) handleAutoIPCallback(chatID int64, param string, parts []string) {
	b.mu.Lock()
	wizard := b.autoWizard
	b.mu.Unlock()

	if wizard == nil {
		b.reply(chatID, "⚠️ 请先使用 /autoip 开始配置")
		return
	}

	// Get the sub-action from parts
	if len(parts) < 3 {
		return
	}
	subAction := parts[1]
	value := parts[2]

	switch subAction {
	case "cancel":
		b.mu.Lock()
		b.autoWizard = nil
		b.mu.Unlock()
		b.reply(chatID, "❌ 已取消自动刷IP配置")

	case "account":
		// Step 1 -> 2
		b.mu.Lock()
		wizard.AccountName = value
		wizard.Step = 2
		b.mu.Unlock()
		b.showPurityStep(chatID)

	case "purity":
		// Step 2 -> 3
		threshold, _ := strconv.Atoi(value)
		b.mu.Lock()
		wizard.PurityThreshold = threshold
		wizard.Step = 3
		b.mu.Unlock()
		b.showNativeStep(chatID)

	case "native":
		// Step 3 -> 4
		b.mu.Lock()
		wizard.NativeRequired = value
		wizard.Step = 4
		b.mu.Unlock()
		b.showMatchModeStep(chatID)

	case "mode":
		// Step 4 -> 5
		b.mu.Lock()
		wizard.MatchMode = value
		wizard.Step = 5
		b.mu.Unlock()
		b.showIntervalStep(chatID)

	case "confirm":
		b.startAutoApplyTask(chatID)

	case "delall":
		// Delete all existing IPs then start
		b.deleteAllIPsAndStart(chatID)

	case "keepstart":
		// Keep existing IPs and start
		b.mu.Lock()
		config := b.autoApply
		client, _ := b.clients[config.AccountName]
		b.mu.Unlock()
		b.doStartAutoApply(chatID, client, config)
	}
}

// showPurityStep shows purity threshold selection (Step 2)
func (b *Bot) showPurityStep(chatID int64) {
	buttons := [][]tgbotapi.InlineKeyboardButton{
		{
			tgbotapi.NewInlineKeyboardButtonData("10%", "autoip:purity:10"),
			tgbotapi.NewInlineKeyboardButtonData("20%", "autoip:purity:20"),
			tgbotapi.NewInlineKeyboardButtonData("30%", "autoip:purity:30"),
		},
		{
			tgbotapi.NewInlineKeyboardButtonData("50%", "autoip:purity:50"),
			tgbotapi.NewInlineKeyboardButtonData("不限", "autoip:purity:100"),
		},
		{tgbotapi.NewInlineKeyboardButtonData("❌ 取消", "autoip:cancel:")},
	}

	msg := tgbotapi.NewMessage(chatID, "🔄 *自动刷IP配置* (2/5)\n\n请选择纯净度阈值 (越低越纯净):")
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

// showNativeStep shows native IP requirement selection (Step 3)
func (b *Bot) showNativeStep(chatID int64) {
	buttons := [][]tgbotapi.InlineKeyboardButton{
		{
			tgbotapi.NewInlineKeyboardButtonData("🏠 原生IP", "autoip:native:原生IP"),
			tgbotapi.NewInlineKeyboardButtonData("📡 非原生IP", "autoip:native:非原生IP"),
		},
		{tgbotapi.NewInlineKeyboardButtonData("🔓 不限", "autoip:native:any")},
		{tgbotapi.NewInlineKeyboardButtonData("❌ 取消", "autoip:cancel:")},
	}

	msg := tgbotapi.NewMessage(chatID, "🔄 *自动刷IP配置* (3/5)\n\n请选择IP来源要求:")
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

// showMatchModeStep shows match mode selection (Step 4)
func (b *Bot) showMatchModeStep(chatID int64) {
	buttons := [][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData("✅ 满足全部条件", "autoip:mode:all")},
		{tgbotapi.NewInlineKeyboardButtonData("☑️ 满足任一条件", "autoip:mode:any")},
		{tgbotapi.NewInlineKeyboardButtonData("❌ 取消", "autoip:cancel:")},
	}

	msg := tgbotapi.NewMessage(chatID, "🔄 *自动刷IP配置* (4/5)\n\n请选择匹配模式:")
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

// showIntervalStep asks for interval input (Step 5)
func (b *Bot) showIntervalStep(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, `🔄 *自动刷IP配置* (5/5)

请输入操作间隔时间 (秒):

• 输入单个数字: `+"`200`"+` 
• 或输入范围: `+"`200-300`"+` (随机等待)

_直接发送消息即可_`)
	msg.ParseMode = tgbotapi.ModeMarkdown
	b.api.Send(msg)
}

// handleIntervalInput handles the interval text input
func (b *Bot) handleIntervalInput(chatID int64, text string) {
	text = strings.TrimSpace(text)

	var minInterval, maxInterval int
	var err error

	if strings.Contains(text, "-") {
		parts := strings.Split(text, "-")
		if len(parts) != 2 {
			b.reply(chatID, "❌ 格式错误，请输入: 200 或 200-300")
			return
		}
		minInterval, err = strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			b.reply(chatID, "❌ 无效的数字: "+parts[0])
			return
		}
		maxInterval, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			b.reply(chatID, "❌ 无效的数字: "+parts[1])
			return
		}
		if minInterval > maxInterval {
			minInterval, maxInterval = maxInterval, minInterval
		}
	} else {
		minInterval, err = strconv.Atoi(text)
		if err != nil {
			b.reply(chatID, "❌ 无效的数字: "+text)
			return
		}
		maxInterval = minInterval
	}

	if minInterval < 10 {
		b.reply(chatID, "❌ 间隔时间不能小于10秒")
		return
	}

	b.mu.Lock()
	wizard := b.autoWizard
	if wizard != nil {
		wizard.Step = 6 // Ready to confirm
	}
	b.mu.Unlock()

	// Show confirmation
	b.showConfirmation(chatID, minInterval, maxInterval)
}

// showConfirmation shows the final confirmation
func (b *Bot) showConfirmation(chatID int64, minInterval, maxInterval int) {
	b.mu.Lock()
	wizard := b.autoWizard
	b.mu.Unlock()

	if wizard == nil {
		b.reply(chatID, "⚠️ 配置已失效，请重新使用 /autoip")
		return
	}

	// Store interval in autoApply config temporarily via wizard
	b.mu.Lock()
	b.autoApply = &AutoApplyConfig{
		AccountName:     wizard.AccountName,
		PurityThreshold: wizard.PurityThreshold,
		NativeRequired:  wizard.NativeRequired,
		MatchMode:       wizard.MatchMode,
		IntervalMin:     minInterval,
		IntervalMax:     maxInterval,
		ChatID:          chatID,
	}
	b.mu.Unlock()

	// Build summary
	purityText := fmt.Sprintf("<= %d%%", wizard.PurityThreshold)
	if wizard.PurityThreshold >= 100 {
		purityText = "不限"
	}

	nativeText := wizard.NativeRequired
	if wizard.NativeRequired == "any" {
		nativeText = "不限"
	}

	modeText := "满足全部条件"
	if wizard.MatchMode == "any" {
		modeText = "满足任一条件"
	}

	intervalText := fmt.Sprintf("%d秒", minInterval)
	if minInterval != maxInterval {
		intervalText = fmt.Sprintf("%d-%d秒 (随机)", minInterval, maxInterval)
	}

	text := fmt.Sprintf(`✅ *确认自动刷IP配置*

📍 *账号:* %s
📊 *纯净度:* %s
🌐 *来源:* %s
🔀 *匹配模式:* %s
⏱ *间隔时间:* %s

确认开始自动刷IP?`, wizard.AccountName, purityText, nativeText, modeText, intervalText)

	buttons := [][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData("▶️ 开始刷IP", "autoip:confirm:")},
		{tgbotapi.NewInlineKeyboardButtonData("❌ 取消", "autoip:cancel:")},
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

// startAutoApplyTask starts the auto-apply background task
func (b *Bot) startAutoApplyTask(chatID int64) {
	b.mu.Lock()
	config := b.autoApply
	if config == nil {
		b.mu.Unlock()
		b.reply(chatID, "⚠️ 配置已失效，请重新使用 /autoip")
		return
	}

	// Get the client for this account
	client, ok := b.clients[config.AccountName]
	if !ok {
		b.mu.Unlock()
		b.reply(chatID, "❌ 账号不存在: "+config.AccountName)
		return
	}
	b.mu.Unlock()

	// Check if there are existing IPs
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ips, err := client.ListReservedIPs(ctx)
	if err != nil {
		b.reply(chatID, "⚠️ 检查IP列表失败: "+err.Error())
		// Continue anyway
		b.doStartAutoApply(chatID, client, config)
		return
	}

	if len(ips) > 0 {
		// Prompt user about existing IPs
		var ipList strings.Builder
		for _, ip := range ips {
			ipList.WriteString(fmt.Sprintf("• `%s`\n", ip.IPAddress))
		}

		text := fmt.Sprintf(`⚠️ *账号 [%s] 已有 %d 个IP:*

%s
请选择操作:`, config.AccountName, len(ips), ipList.String())

		buttons := [][]tgbotapi.InlineKeyboardButton{
			{tgbotapi.NewInlineKeyboardButtonData("🗑 删除全部后开始", "autoip:delall:")},
			{tgbotapi.NewInlineKeyboardButtonData("▶️ 保留并继续", "autoip:keepstart:")},
			{tgbotapi.NewInlineKeyboardButtonData("❌ 取消", "autoip:cancel:")},
		}

		msg := tgbotapi.NewMessage(chatID, text)
		msg.ParseMode = tgbotapi.ModeMarkdown
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
		b.api.Send(msg)
		return
	}

	// No existing IPs, start directly
	b.doStartAutoApply(chatID, client, config)
}

// doStartAutoApply actually starts the auto-apply task (called after IP check)
func (b *Bot) doStartAutoApply(chatID int64, client *oci.Client, config *AutoApplyConfig) {
	b.mu.Lock()
	// Create cancelable context
	ctx, cancel := context.WithCancel(context.Background())
	config.Cancel = cancel
	config.Active = true
	config.ChatID = chatID
	b.autoWizard = nil // Clear wizard
	b.mu.Unlock()

	b.reply(chatID, fmt.Sprintf("🚀 *自动刷IP已启动*\n\n账号: %s\n使用 /stopauto 停止", config.AccountName))

	// Start background task
	go b.runAutoApplyTask(ctx, client, config)
}

// deleteAllIPsAndStart deletes all existing IPs then starts auto-apply
func (b *Bot) deleteAllIPsAndStart(chatID int64) {
	b.mu.Lock()
	config := b.autoApply
	if config == nil {
		b.mu.Unlock()
		b.reply(chatID, "⚠️ 配置已失效，请重新使用 /autoip")
		return
	}

	client, ok := b.clients[config.AccountName]
	if !ok {
		b.mu.Unlock()
		b.reply(chatID, "❌ 账号不存在: "+config.AccountName)
		return
	}
	intervalMin := config.IntervalMin
	intervalMax := config.IntervalMax
	b.mu.Unlock()

	// List and delete all IPs
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ips, err := client.ListReservedIPs(ctx)
	cancel()

	if err != nil {
		b.reply(chatID, "❌ 获取IP列表失败: "+err.Error())
		return
	}

	for i, ip := range ips {
		b.reply(chatID, fmt.Sprintf("🗑 删除IP (%d/%d): %s", i+1, len(ips), ip.IPAddress))

		delCtx, delCancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.DeleteReservedIP(delCtx, ip.ID)
		delCancel()

		if err != nil {
			b.reply(chatID, fmt.Sprintf("⚠️ 删除失败: %s", err.Error()))
		}

		// Wait interval after delete
		if i < len(ips)-1 {
			interval := intervalMin
			if intervalMax > intervalMin {
				interval = intervalMin + rand.Intn(intervalMax-intervalMin+1)
			}
			b.reply(chatID, fmt.Sprintf("⏳ 等待 %d 秒...", interval))
			time.Sleep(time.Duration(interval) * time.Second)
		}
	}

	b.reply(chatID, "✅ 已删除所有IP，开始自动刷IP...")

	// Start auto-apply
	b.doStartAutoApply(chatID, client, config)
}

// stopAutoApply stops the running auto-apply task
func (b *Bot) stopAutoApply(chatID int64) {
	b.mu.Lock()
	config := b.autoApply
	if config == nil || !config.Active {
		b.mu.Unlock()
		b.reply(chatID, "⚠️ 当前没有运行中的自动刷IP任务")
		return
	}

	if config.Cancel != nil {
		config.Cancel()
	}
	config.Active = false
	b.autoApply = nil
	b.mu.Unlock()

	b.reply(chatID, "⏹ 已停止自动刷IP任务")
}

// runAutoApplyTask runs the auto-apply background loop
func (b *Bot) runAutoApplyTask(ctx context.Context, client *oci.Client, config *AutoApplyConfig) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			log.Println("Auto-apply task cancelled")
			return
		default:
		}

		attempt++
		log.Printf("Auto-apply attempt %d", attempt)

		// Step 1: Create IP
		log.Printf("Creating reserved IP (attempt %d)...", attempt)

		createCtx, createCancel := context.WithTimeout(ctx, 2*time.Minute)
		displayName := fmt.Sprintf("auto-%d", time.Now().Unix())
		publicIP, err := client.CreateReservedIP(createCtx, displayName)
		createCancel()

		if err != nil {
			log.Printf("Create failed: %s. Waiting...", err.Error())
			b.waitInterval(ctx, config)
			continue
		}

		// Wait for IP ready
		waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
		publicIP, err = client.WaitForIPReady(waitCtx, publicIP.ID, 60*time.Second)
		waitCancel()

		if err != nil {
			log.Printf("Wait for IP ready failed: %s", err.Error())
			b.waitInterval(ctx, config)
			continue
		}

		// Step 2: Check IP purity immediately
		log.Printf("IP created: %s. Checking purity...", publicIP.IPAddress)

		checkCtx, checkCancel := context.WithTimeout(ctx, 60*time.Second)
		info, err := ippure.Check(checkCtx, publicIP.IPAddress)
		checkCancel()

		if err != nil {
			log.Printf("Check failed: %s. Keeping IP and continuing...", err.Error())
			// Optional: notify user if check fails repeatedly? For now just log.
			b.waitInterval(ctx, config)
			continue
		}

		// Step 3: Check if it matches criteria
		match := b.checkIPMatch(info, config)

		if match {
			// Found matching IP!
			b.mu.Lock()
			b.purityCache[publicIP.IPAddress] = &IPPurityCache{
				PurityScore: info.PurityScore,
				IPType:      info.IPType,
				IsNative:    info.IsNative,
			}
			config.Active = false
			b.autoApply = nil
			b.mu.Unlock()

			// Send success notification
			text := fmt.Sprintf(`🎉 *找到符合条件的IP!*

📊 *纯净度:* %s (%s)
🏢 *类型:* %s
🌐 *来源:* %s
🔢 *尝试次数:* %d`,
				info.PurityScore, info.PurityLevel,
				info.IPType,
				info.IsNative,
				attempt)

			b.replyMarkdown(config.ChatID, text)
			log.Printf("Auto-apply found matching IP: %s", publicIP.IPAddress)

			// Show IP list with the new IP highlighted
			b.showIPListWithHighlight(config.ChatID, publicIP.IPAddress, client)
			return
		}

		// Not matching - delete and retry
		log.Printf("IP mismatch (%s/%s). Deleting...", info.PurityScore, info.IsNative)

		delCtx, delCancel := context.WithTimeout(ctx, 30*time.Second)
		err = client.DeleteReservedIP(delCtx, publicIP.ID)
		delCancel()

		if err != nil {
			log.Printf("Delete failed: %s", err.Error())
		}

		// Wait interval before next attempt
		b.waitInterval(ctx, config)
	}
}

// checkIPMatch checks if the IP matches the configured criteria
func (b *Bot) checkIPMatch(info *ippure.IPInfo, config *AutoApplyConfig) bool {
	// Parse purity score (remove % if present)
	purityStr := strings.TrimSuffix(info.PurityScore, "%")
	purity, err := strconv.Atoi(purityStr)
	if err != nil {
		purity = 100 // Default to not matching
	}

	purityOK := purity <= config.PurityThreshold
	nativeOK := config.NativeRequired == "any" || info.IsNative == config.NativeRequired

	if config.MatchMode == "all" {
		return purityOK && nativeOK
	}
	// mode == "any"
	return purityOK || nativeOK
}

// waitInterval waits for the configured interval
func (b *Bot) waitInterval(ctx context.Context, config *AutoApplyConfig) {
	interval := config.IntervalMin
	if config.IntervalMax > config.IntervalMin {
		interval = config.IntervalMin + rand.Intn(config.IntervalMax-config.IntervalMin+1)
	}

	log.Printf("Waiting %d seconds before next attempt", interval)

	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(interval) * time.Second):
	}
}

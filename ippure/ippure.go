package ippure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// IPInfo contains IP purity check results
type IPInfo struct {
	IPAddress   string // IP address
	PurityScore string // Purity score, e.g. "7%"
	PurityLevel string // Purity level, e.g. "极其纯净"
	IPType      string // IP type: 机房IP / 住宅IP
	IsNative    string // IP origin: 原生IP / 非原生IP
}

// Check checks IP purity via ippure.com
func Check(ctx context.Context, ip string) (*IPInfo, error) {
	// Chrome options for headless browsing
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	// Create headless Chrome context
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	chromeCtx, chromeCancel := chromedp.NewContext(allocCtx)
	defer chromeCancel()

	// Set timeout for the entire operation
	chromeCtx, cancel := context.WithTimeout(chromeCtx, 60*time.Second)
	defer cancel()

	url := "https://ippure.com/"

	var purityText string

	// JavaScript to extract IP info
	extractJS := `
	(() => {
		const result = {
			purity: '',
			purityLevel: '',
			ipType: '',
			native: ''
		};
		
		const allText = document.body.innerText;
		
		// Match IPPure系数: format is "IPPure系数\n7% 极度纯净" or similar
		const purityMatch = allText.match(/IPPure系数\s*\n?\s*(\d+)%\s*([^\n]*)/);
		if (purityMatch) {
			result.purity = purityMatch[1] + '%';
			result.purityLevel = purityMatch[2].trim();
		}
		
		// Match IP属性: format is "IP属性\n机房IP"
		const attrMatch = allText.match(/IP属性\s*\n?\s*(机房IP|住宅IP|Data Center|Residential)/);
		if (attrMatch) {
			result.ipType = attrMatch[1];
		}
		
		// Match IP来源: format is "IP来源\n原生IP"
		const nativeMatch = allText.match(/IP来源\s*\n?\s*(原生IP|非原生IP|广播IP|Native IP|Broadcast)/);
		if (nativeMatch) {
			result.native = nativeMatch[1];
		}
		
		return JSON.stringify(result);
	})()`

	err := chromedp.Run(chromeCtx,
		// Navigate to the site
		chromedp.Navigate(url),
		chromedp.Sleep(3*time.Second),
		// Click on the search input to focus it
		chromedp.Click(`input`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		// Select all existing text and type the IP with Enter to submit
		chromedp.Evaluate(`
			(() => {
				const input = document.querySelector('input');
				if (input) {
					input.focus();
					input.select();
				}
			})()
		`, nil),
		chromedp.SendKeys(`input`, ip+"\r", chromedp.ByQuery),
		// Wait for results to load
		chromedp.Sleep(10*time.Second),
		// Extract the results
		chromedp.Evaluate(extractJS, &purityText),
	)
	if err != nil {
		return nil, fmt.Errorf("browser automation failed: %w", err)
	}

	// Parse JSON result
	info := &IPInfo{IPAddress: ip}

	// Simple JSON parsing without external lib
	purityText = strings.TrimPrefix(purityText, `"`)
	purityText = strings.TrimSuffix(purityText, `"`)
	purityText = strings.ReplaceAll(purityText, `\"`, `"`)

	// Extract purity
	if idx := strings.Index(purityText, `"purity":"`); idx != -1 {
		start := idx + len(`"purity":"`)
		end := strings.Index(purityText[start:], `"`)
		if end != -1 {
			info.PurityScore = purityText[start : start+end]
		}
	}

	// Extract purityLevel
	if idx := strings.Index(purityText, `"purityLevel":"`); idx != -1 {
		start := idx + len(`"purityLevel":"`)
		end := strings.Index(purityText[start:], `"`)
		if end != -1 {
			info.PurityLevel = purityText[start : start+end]
		}
	}

	// Extract ipType
	if idx := strings.Index(purityText, `"ipType":"`); idx != -1 {
		start := idx + len(`"ipType":"`)
		end := strings.Index(purityText[start:], `"`)
		if end != -1 {
			info.IPType = purityText[start : start+end]
		}
	}

	// Extract native
	if idx := strings.Index(purityText, `"native":"`); idx != -1 {
		start := idx + len(`"native":"`)
		end := strings.Index(purityText[start:], `"`)
		if end != -1 {
			info.IsNative = purityText[start : start+end]
		}
	}

	// Set defaults for empty fields
	if info.PurityScore == "" {
		info.PurityScore = "未知"
	}
	if info.PurityLevel == "" {
		info.PurityLevel = "未知"
	}
	if info.IPType == "" {
		info.IPType = "未知"
	}
	if info.IsNative == "" {
		info.IsNative = "未知"
	}

	return info, nil
}

// FormatResult formats IPInfo as a readable string
func (info *IPInfo) FormatResult() string {
	return fmt.Sprintf(`🔍 IP 纯净度检测

IP: %s

📊 纯净度: %s (%s)
🏢 类型: %s
🌐 来源: %s`,
		info.IPAddress,
		info.PurityScore, info.PurityLevel,
		info.IPType,
		info.IsNative)
}

package main

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"time"

	"github.com/chromedp/chromedp"
)

// macOS User-Agent 列表
var macUserAgents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Safari/605.1.15",
}

// Windows User-Agent 列表
var windowsUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36",
}

// Linux User-Agent 列表
var linuxUserAgents = []string{
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// randomUA 根据当前操作系统返回匹配的 User-Agent
func randomUA() string {
	var userAgents []string
	
	switch runtime.GOOS {
	case "darwin":
		userAgents = macUserAgents
	case "windows":
		userAgents = windowsUserAgents
	case "linux":
		userAgents = linuxUserAgents
	default:
		userAgents = macUserAgents // 默认使用 macOS
	}
	
	return userAgents[rand.Intn(len(userAgents))]
}

// AntiDetectScript 返回反检测 JavaScript
func AntiDetectScript() string {
	return `
		// 覆盖 webdriver 属性
		Object.defineProperty(navigator, 'webdriver', {
			get: () => undefined
		});
		
		// 添加 plugins
		Object.defineProperty(navigator, 'plugins', {
			get: () => [
				{ name: "Chrome PDF Plugin", filename: "internal-pdf-viewer", description: "Portable Document Format" },
				{ name: "Chrome PDF Viewer", filename: "mhjfbmdgcfjbbpaeojofohoefgiehjai", description: "Portable Document Format" },
				{ name: "Native Client", filename: "internal-nacl-plugin", description: "Native Client module" }
			]
		});
		
		// 添加 languages
		Object.defineProperty(navigator, 'languages', {
			get: () => ['zh-CN', 'zh', 'en']
		});
		
		// 覆盖 chrome 对象
		window.chrome = {
			runtime: {
				OnInstalledReason: { CHROME_UPDATE: "chrome_update", UPDATE: "update", INSTALL: "install" },
				OnRestartRequiredReason: { APP_UPDATE: "app_update", OS_UPDATE: "os_update", PERIODIC: "periodic" },
				PlatformArch: { ARM: "arm", ARM64: "arm64", MIPS: "mips", MIPS64: "mips64", MIPS64EL: "mips64el", MIPSEL: "mipsel", X86_32: "x86-32", X86_64: "x86-64" },
				PlatformNaclArch: { ARM: "arm", MIPS: "mips", MIPS64: "mips64", MIPS64EL: "mips64el", MIPSEL: "mipsel", X86_32: "x86-32", X86_64: "x86-64" },
				PlatformOs: { ANDROID: "android", CROS: "cros", LINUX: "linux", MAC: "mac", OPENBSD: "openbsd", WIN: "win" },
				RequestUpdateCheckStatus: { NO_UPDATE: "no_update", THROTTLED: "throttled", UPDATE_AVAILABLE: "update_available" }
			}
		};
		
		// 覆盖 permission 查询
		const originalQuery = window.navigator.permissions.query;
		window.navigator.permissions.query = (parameters) => (
			parameters.name === 'notifications' 
			? Promise.resolve({ state: Notification.permission })
			: originalQuery(parameters)
		);
		
		// 删除 webdriver 痕迹
		delete navigator.__proto__.webdriver;
	`
}

// HumanLikeBehavior 模拟人类行为
func HumanLikeBehavior(ctx context.Context) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		// 随机滚动代替鼠标移动（chromedp 没有直接的 MouseMoveXY）
		scrollY := rand.Intn(300) + 100
		
		script := fmt.Sprintf(`window.scrollBy(0, %d);`, scrollY)
		
		actions := []chromedp.Action{
			chromedp.Evaluate(script, nil),
			chromedp.Sleep(time.Duration(rand.Intn(500)+200) * time.Millisecond),
		}
		
		for _, a := range actions {
			if err := a.Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// RandomSleep 随机延迟
func RandomSleep(minMs, maxMs int) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		delay := time.Duration(rand.Intn(maxMs-minMs)+minMs) * time.Millisecond
		time.Sleep(delay)
		return nil
	}
}

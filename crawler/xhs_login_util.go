package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/chromedp/chromedp"
)

// XHSLogin 小红书自动登录
func XHSLogin(ctx context.Context, username, password string) error {
	log.Println("🔐 开始小红书自动登录...")
	
	// 访问登录页
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://www.xiaohongshu.com"),
		chromedp.Sleep(2*time.Second),
	); err != nil {
		return fmt.Errorf("访问首页失败: %w", err)
	}
	
	// 点击登录按钮
	var loginBtnVisible bool
	chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('text=登录')`, &loginBtnVisible))
	
	if loginBtnVisible {
		log.Println("📝 点击登录按钮...")
		if err := chromedp.Run(ctx,
			chromedp.Click("text=登录", chromedp.NodeVisible),
			chromedp.Sleep(1*time.Second),
		); err != nil {
			// 尝试其他选择器
			chromedp.Run(ctx, chromedp.Click(".login-btn", chromedp.NodeVisible))
		}
	}
	
	log.Println("⏳ 请在弹出的浏览器中完成登录...")
	log.Println("   建议使用手机验证码登录")
	
	// 等待登录完成（最多60秒）
	start := time.Now()
	for time.Since(start) < 60*time.Second {
		var isLoggedIn bool
		err := chromedp.Run(ctx, chromedp.Evaluate(`
			!document.querySelector('text=登录') && 
			!document.querySelector('.login-btn') &&
			document.cookie.includes('web_session')
		`, &isLoggedIn))
		
		if err == nil && isLoggedIn {
			log.Println("✅ 登录成功！")
			return nil
		}
		
		time.Sleep(2 * time.Second)
	}
	
	return fmt.Errorf("登录超时，请手动确认是否登录成功")
}

// WaitForLogin 等待用户手动登录
func WaitForLogin(ctx context.Context, timeout time.Duration) error {
	log.Println("⏳ 等待登录完成，请在浏览器中完成登录...")
	log.Printf("   超时时间: %v\n", timeout)
	
	start := time.Now()
	for time.Since(start) < timeout {
		var isLoggedIn bool
		err := chromedp.Run(ctx, chromedp.Evaluate(`
			(function() {
				// 检查是否有登录态 cookie
				var hasSession = document.cookie.includes('web_session') || 
								document.cookie.includes('session');
				// 检查页面是否有用户头像或昵称（已登录特征）
				var hasUserInfo = !!document.querySelector('.user-info') ||
								!!document.querySelector('.avatar') ||
								!!document.querySelector('[class*="nickname"]');
				// 检查是否还有登录按钮
				var hasLoginBtn = !!document.querySelector('text=登录') ||
								!!document.querySelector('.login-btn');
				
				return hasSession || (hasUserInfo && !hasLoginBtn);
			})()
		`, &isLoggedIn))
		
		if err == nil && isLoggedIn {
			log.Println("✅ 检测到登录状态！")
			return nil
		}
		
		time.Sleep(1 * time.Second)
	}
	
	return fmt.Errorf("等待登录超时")
}

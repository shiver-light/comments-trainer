#!/usr/bin/env python3
"""
小红书自动登录并导出 Cookie
用法: python3 xhs_login.py
"""

import json
import time
import os
import sys
from pathlib import Path

try:
    from playwright.sync_api import sync_playwright, expect
except ImportError:
    print("❌ 请先安装 Playwright:")
    print("   pip3 install playwright")
    print("   python3 -m playwright install chromium")
    sys.exit(1)

def save_cookies(cookies, cookie_file):
    """保存 Cookie 到文件"""
    cookie_file.parent.mkdir(parents=True, exist_ok=True)
    
    # 转换 Playwright cookie 格式为标准格式
    formatted_cookies = []
    for c in cookies:
        formatted_cookies.append({
            "name": c.get("name", ""),
            "value": c.get("value", ""),
            "domain": c.get("domain", ""),
            "path": c.get("path", "/"),
            "httpOnly": c.get("httpOnly", False),
            "secure": c.get("secure", False),
            "sameSite": c.get("sameSite", "Lax")
        })
    
    with open(cookie_file, 'w', encoding='utf-8') as f:
        json.dump(formatted_cookies, f, ensure_ascii=False, indent=2)
    
    return formatted_cookies

def check_login_status(cookies):
    """检查是否有登录态 Cookie"""
    login_indicators = ['web_session', 'session', 'ticket', 'token', 'login']
    login_cookies = [c for c in cookies if any(ind in c.get('name', '').lower() for ind in login_indicators)]
    return len(login_cookies) > 0, login_cookies

def main():
    cookie_file = Path("cookies/xhs.json")
    
    print("🚀 启动小红书自动登录工具")
    print("=" * 50)
    
    with sync_playwright() as p:
        # 启动浏览器（有头模式，方便用户操作）
        print("🌐 启动浏览器...")
        browser = p.chromium.launch(
            headless=False,
            args=[
                '--disable-blink-features=AutomationControlled',
                '--disable-web-security',
                '--window-size=1280,800'
            ]
        )
        
        context = browser.new_context(
            viewport={'width': 1280, 'height': 800},
            user_agent='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
        )
        
        page = context.new_page()
        
        # 访问小红书
        print("🌐 访问小红书...")
        page.goto("https://www.xiaohongshu.com", wait_until="domcontentloaded")
        
        # 等待页面加载
        time.sleep(2)
        
        # 检查是否已经登录（通过查看是否有登录按钮）
        try:
            # 尝试找登录按钮
            login_btn = page.locator('text=登录').first
            if login_btn.is_visible(timeout=3000):
                print("\n" + "=" * 50)
                print("📱 请在浏览器中完成登录")
                print("=" * 50)
                print("1. 点击页面上的【登录】按钮")
                print("2. 选择登录方式（推荐：手机号 + 验证码）")
                print("3. 完成登录后，回到这里按回车键")
                print("=" * 50 + "\n")
                input("⏳ 完成登录后按回车键继续...")
            else:
                print("✅ 检测到已登录状态")
        except:
            print("⚠️ 无法检测登录状态，请手动确认")
            input("⏳ 完成登录后按回车键继续...")
        
        # 等待页面稳定
        print("⏳ 等待页面稳定...")
        time.sleep(3)
        
        # 验证是否登录成功 - 尝试访问搜索页
        print("🔍 验证登录状态...")
        page.goto("https://www.xiaohongshu.com/search_result?keyword=test", wait_until="domcontentloaded")
        time.sleep(2)
        
        # 检查页面内容
        page_content = page.content()
        if "登录" in page_content and "登录后查看" in page_content:
            print("❌ 登录验证失败：页面仍显示登录提示")
            print("💡 提示：请确保在浏览器中成功登录")
            browser.close()
            return False
        
        print("✅ 登录验证通过！")
        
        # 获取 Cookie
        print("📋 导出 Cookie...")
        cookies = context.cookies()
        
        # 保存 Cookie
        formatted_cookies = save_cookies(cookies, cookie_file)
        
        # 检查登录态
        has_login, login_cookies = check_login_status(formatted_cookies)
        
        print(f"\n📊 导出结果:")
        print(f"   总 Cookie 数: {len(formatted_cookies)}")
        print(f"   登录态 Cookie: {len(login_cookies)}")
        
        if login_cookies:
            print(f"   登录态标识: {', '.join([c['name'] for c in login_cookies])}")
        
        if has_login:
            print(f"\n✅ Cookie 已保存到: {cookie_file.absolute()}")
            print("\n🧪 现在可以测试爬虫:")
            print(f"   ./crawler_test -keywords '咖啡' -platforms xhs -engine chromedp -maxPages 1")
        else:
            print("\n⚠️ 警告: 未检测到登录态 Cookie")
            print("   可能登录未成功，请重试")
        
        browser.close()
        return has_login

if __name__ == "__main__":
    try:
        success = main()
        sys.exit(0 if success else 1)
    except KeyboardInterrupt:
        print("\n\n⚠️ 用户取消操作")
        sys.exit(1)
    except Exception as e:
        print(f"\n❌ 错误: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)

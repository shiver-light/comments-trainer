#!/usr/bin/env python3
"""
小红书自动登录并导出 Cookie
用法: python3 xhs_login.py
"""

import json
import time
import os
from pathlib import Path

def export_cookies_manual():
    """
    引导用户手动导出 Cookie 的说明
    """
    print("""
╔══════════════════════════════════════════════════════════════╗
║           小红书 Cookie 导出指南                              ║
╠══════════════════════════════════════════════════════════════╣
║                                                              ║
║  方法：使用浏览器 DevTools（推荐）                           ║
║                                                              ║
║  1. 用 Chrome 访问 https://www.xiaohongshu.com               ║
║  2. 登录你的账号（手机号/微信/微博）                         ║
║  3. 按 F12 打开 DevTools → Console 标签                    ║
║  4. 粘贴以下代码并回车：                                     ║
║                                                              ║
║     copy(JSON.stringify(document.cookie.split(';').map(c=>{  ║
║       const [n,v]=c.trim().split('=');                       ║
║       return {name:n,value:v,domain:'.xiaohongshu.com',      ║
║       path:'/'};                                             ║
║     }), null, 2))                                            ║
║                                                              ║
║  5. Cookie JSON 已复制到剪贴板                               ║
║  6. 创建文件 crawler/cookies/xhs.json 并粘贴保存           ║
║                                                              ║
╠══════════════════════════════════════════════════════════════╣
║  或者使用 Playwright 自动方案（需要安装）：                  ║
║     pip3 install playwright                                  ║
║     python3 -m playwright install chromium                   ║
║     python3 xhs_login.py --auto                              ║
║                                                              ║
╚══════════════════════════════════════════════════════════════╝
""")

def auto_login_with_playwright():
    """
    使用 Playwright 自动登录
    """
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        print("❌ 请先安装 Playwright: pip3 install playwright")
        print("   然后运行: python3 -m playwright install chromium")
        return False
    
    cookie_file = Path("cookies/xhs.json")
    cookie_file.parent.mkdir(parents=True, exist_ok=True)
    
    print("🚀 启动浏览器...")
    
    with sync_playwright() as p:
        # 启动浏览器（有头模式，方便用户操作）
        browser = p.chromium.launch(
            headless=False,
            args=['--disable-blink-features=AutomationControlled']
        )
        
        context = browser.new_context(
            viewport={'width': 1280, 'height': 800},
            user_agent='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
        )
        
        page = context.new_page()
        
        # 访问小红书
        print("🌐 访问小红书...")
        page.goto("https://www.xiaohongshu.com", wait_until="networkidle")
        
        # 等待用户登录
        print("""
╔══════════════════════════════════════════════════════════════╗
║  请在浏览器中完成登录                                        ║
║                                                              ║
║  1. 点击页面上的"登录"按钮                                   ║
║  2. 选择登录方式（推荐手机号 + 验证码）                     ║
║  3. 完成登录后，按回车键继续...                              ║
║                                                              ║
╚══════════════════════════════════════════════════════════════╝
        """)
        
        input("⏳ 完成登录后按回车键...")
        
        # 等待页面稳定
        time.sleep(2)
        
        # 获取 Cookie
        cookies = context.cookies()
        
        # 过滤小红书相关的 Cookie
        xhs_cookies = [
            {
                "name": c["name"],
                "value": c["value"],
                "domain": c["domain"],
                "path": c["path"],
                "httpOnly": c.get("httpOnly", False),
                "secure": c.get("secure", False),
                "sameSite": c.get("sameSite", "unspecified")
            }
            for c in cookies
            if "xiaohongshu" in c.get("domain", "") or "xhs" in c.get("domain", "")
        ]
        
        if not xhs_cookies:
            print("⚠️ 未检测到小红书 Cookie，请确认已登录")
            browser.close()
            return False
        
        # 保存 Cookie
        with open(cookie_file, 'w', encoding='utf-8') as f:
            json.dump(xhs_cookies, f, ensure_ascii=False, indent=2)
        
        print(f"✅ Cookie 已保存到: {cookie_file.absolute()}")
        print(f"📊 共导出 {len(xhs_cookies)} 个 Cookie")
        
        # 验证登录状态
        print("🔍 验证登录状态...")
        page.goto("https://www.xiaohongshu.com/search_result?keyword=test", wait_until="networkidle")
        time.sleep(2)
        
        if "登录" not in page.content() and "login" not in page.content().lower():
            print("✅ 登录状态验证通过！")
        else:
            print("⚠️ 可能未登录成功，请检查")
        
        browser.close()
        return True

def main():
    import sys
    
    if len(sys.argv) > 1 and sys.argv[1] == "--auto":
        # 自动模式
        success = auto_login_with_playwright()
        if not success:
            export_cookies_manual()
    else:
        # 默认显示手动指南
        export_cookies_manual()
        
        # 询问是否尝试自动模式
        print("\n是否尝试自动登录模式？(需要安装 Playwright)")
        choice = input("输入 y 继续，或其他键退出: ").strip().lower()
        
        if choice == 'y':
            auto_login_with_playwright()

if __name__ == "__main__":
    main()

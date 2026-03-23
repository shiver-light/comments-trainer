#!/bin/bash
# 一键获取小红书 Cookie 脚本

echo "🍠 小红书 Cookie 获取工具"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "步骤："
echo ""
echo "1. 打开 Chrome 访问 https://www.xiaohongshu.com"
echo ""
echo "2. 点击登录 → 选择手机号登录"
echo "   输入手机号 → 获取验证码 → 完成登录"
echo ""
echo "3. 登录成功后，按 F12 打开控制台(Console)"
echo ""
echo "4. 粘贴以下代码并回车："
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
cat << 'JSCODE'
const cookies = await (async () => {
    // 获取所有 cookie
    const cks = document.cookie.split(';').map(c => {
        const [name, ...valueParts] = c.trim().split('=');
        return {
            name: name.trim(),
            value: valueParts.join('='),
            domain: location.hostname.includes('xiaohongshu.com') ? '.xiaohongshu.com' : location.hostname,
            path: '/',
            httpOnly: false,
            secure: location.protocol === 'https:',
            sameSite: 'Lax'
        };
    });
    
    // 检查是否有登录态 cookie
    const sessionCookie = cks.find(c => c.name.includes('session') || c.name.includes('ticket'));
    if (sessionCookie) {
        console.log('✅ 检测到登录态 Cookie:', sessionCookie.name);
    } else {
        console.log('⚠️ 警告: 未检测到登录态 Cookie，请确认已登录');
    }
    
    const json = JSON.stringify(cks, null, 2);
    await navigator.clipboard.writeText(json);
    console.log('📋 Cookie 已复制到剪贴板，共', cks.length, '个');
    return json;
})();
JSCODE
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "5. 回到终端运行："
echo "   cd ~/src/comments-trainer/crawler"
echo "   mkdir -p cookies"
echo "   pbpaste > cookies/xhs.json"
echo ""
echo "6. 验证 Cookie 是否有效："
echo "   cat cookies/xhs.json | grep -E 'web_session|ticket|token'"
echo ""
echo "⚠️  Cookie 有效期约 2-4 小时，过期后需重新导出"
echo ""

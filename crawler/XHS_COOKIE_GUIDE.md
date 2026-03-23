# 小红书 Cookie 导出工具

## 🚀 最简单的方法：浏览器控制台

### 步骤 1：登录小红书
```bash
open "https://www.xiaohongshu.com"
```
在浏览器中登录你的账号

### 步骤 2：打开 DevTools
- 按 `F12` 或 `Cmd+Option+I` (Mac)
- 切换到 **Console** 标签

### 步骤 3：粘贴以下代码

```javascript
// 导出小红书 Cookie 为 JSON 格式
(function() {
    const cookies = document.cookie.split(';').map(c => {
        const [name, ...valueParts] = c.trim().split('=');
        const value = valueParts.join('='); // 处理 value 中可能有的 =
        return {
            name: name.trim(),
            value: value,
            domain: location.hostname.includes('xiaohongshu.com') ? '.xiaohongshu.com' : location.hostname,
            path: '/',
            httpOnly: false,
            secure: location.protocol === 'https:',
            sameSite: 'unspecified'
        };
    });
    
    const jsonStr = JSON.stringify(cookies, null, 2);
    
    // 复制到剪贴板
    navigator.clipboard.writeText(jsonStr).then(() => {
        console.log('✅ Cookie 已复制到剪贴板！');
        console.log('📋 内容预览：');
        console.log(jsonStr.slice(0, 500) + '...');
    }).catch(err => {
        console.log('请手动复制以下内容：');
        console.log(jsonStr);
    });
    
    return jsonStr;
})();
```

### 步骤 4：保存文件

在终端中运行：
```bash
cd ~/src/comments-trainer/crawler
mkdir -p cookies

# 创建文件并粘贴剪贴板内容
nano cookies/xhs.json
# 粘贴后按 Ctrl+O, Enter, Ctrl+X 保存
```

或者使用 pbpaste (Mac)：
```bash
pbpaste > cookies/xhs.json
```

---

## 🤖 自动登录方案（Playwright）

### 安装依赖
```bash
pip3 install playwright
python3 -m playwright install chromium
```

### 运行自动登录
```bash
cd ~/src/comments-trainer/crawler
python3 xhs_login.py --auto
```

---

## ⚠️ 注意事项

1. **Cookie 有效期**：小红书的 Cookie 通常几小时后过期，需要定期更新
2. **账号安全**：不要将 Cookie 分享给他人，它可以用来登录你的账号
3. **频率限制**：抓取时不要过于频繁，建议 `rate_per_sec: 0.2`

---

## 🔧 验证 Cookie 是否有效

导出后测试：
```bash
cd ~/src/comments-trainer/crawler
./crawler_test \
  -keywords "咖啡" \
  -platforms "xhs" \
  -engine "chromedp" \
  -maxPages 1 \
  -out xhs_test.csv
```

如果看到 `[xhs] ✓ Cookie 验证通过`，说明成功了！

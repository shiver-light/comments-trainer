#!/bin/bash
# 小红书登录并立即抓取
# 用法: ./xhs_login_and_crawl.sh <关键词>

KEYWORD=${1:-"咖啡"}

echo "🚀 小红书登录+抓取工具"
echo "======================="
echo ""
echo "这个过程会："
echo "1. 打开浏览器让你登录小红书"
echo "2. 登录后立即抓取数据"
echo ""
echo "请按回车键开始..."
read

cd "$(dirname "$0")"

# 先尝试直接抓取
echo "🔍 尝试使用现有 Cookie 抓取..."
./crawler_test \
    -keywords "$KEYWORD" \
    -platforms "xhs" \
    -engine "chromedp" \
    -maxPages 1 \
    -concurrency 1 \
    -out "xhs_${KEYWORD}.csv" 2>&1 | tee /tmp/crawl.log

# 检查是否成功
if grep -q "写出 [1-9]" /tmp/crawl.log; then
    echo ""
    echo "✅ 抓取成功！"
    echo "📄 结果文件: xhs_${KEYWORD}.csv / xhs_${KEYWORD}.jsonl"
    exit 0
fi

# 如果失败，提示登录
echo ""
echo "❌ Cookie 无效，需要登录"
echo ""
echo "请运行以下命令登录："
echo "  ./crawler_test -login -platforms xhs"
echo ""
echo "登录完成后，再运行："
echo "  ./crawler_test -keywords '$KEYWORD' -platforms xhs -engine chromedp -out xhs_${KEYWORD}.csv"

#!/bin/bash
# 调试小红书页面结构
# 用法: ./debug_xhs.sh

echo "🐛 小红书页面调试工具"
echo "======================"
echo ""
echo "这个脚本会："
echo "1. 登录小红书"
echo "2. 访问搜索页并保存 HTML"
echo "3. 分析页面结构"
echo ""

cd "$(dirname "$0")"

# 检查是否需要重新编译
if [ ! -f crawler_test ] || [ crawler_test -ot main.go ]; then
    echo "🔨 编译中..."
    go build -o crawler_test .
fi

# 创建调试输出目录
mkdir -p debug

# 修改 main.go 添加调试模式（临时方案）
echo ""
echo "请使用以下命令手动调试："
echo ""
echo "1. 登录小红书："
echo "   ./crawler_test -login -platforms xhs"
echo ""
echo "2. 登录成功后，在浏览器中访问："
echo "   https://www.xiaohongshu.com/search_result?keyword=咖啡&type=51"
echo ""
echo "3. 按 F12 打开开发者工具，在 Console 中运行："
echo ""
cat << 'JS'
// 保存页面 HTML
copy(document.documentElement.outerHTML);
console.log('HTML 已复制到剪贴板');

// 或者查看笔记列表的选择器
console.log('笔记数量:', document.querySelectorAll('section.note-item').length);
console.log('笔记数量2:', document.querySelectorAll('div.note-item').length);
console.log('笔记数量3:', document.querySelectorAll('[class*="note"]').length);

// 查看第一个笔记的结构
const firstNote = document.querySelector('section.note-item') || document.querySelector('div.note-item');
if (firstNote) {
    console.log('第一个笔记的 HTML:', firstNote.outerHTML.substring(0, 500));
}
JS
echo ""
echo "4. 将复制的 HTML 保存到文件："
echo "   pbpaste > debug/xhs_page.html"
echo ""
echo "5. 查看实际的选择器，然后更新 config.yml"
echo ""

package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

/* =========================
   配置结构
========================= */

type Config struct {
	Global struct {
		UserAgent  string  `yaml:"user_agent"`
		TimeoutSec int     `yaml:"timeout_sec"`
		RatePerSec float64 `yaml:"rate_per_sec"`
	} `yaml:"global"`
	Platforms map[string]PlatformConfig `yaml:"platforms"`
}

type PlatformConfig struct {
	Engine         string   `yaml:"engine"` // http | chromedp
	AllowedDomains []string `yaml:"allowed_domains"`
	CookieFile     string   `yaml:"cookie_file"`
	StartURLs      []string `yaml:"start_urls"`
	List           struct {
		ItemSelector string `yaml:"item_selector"`
		ItemAttr     string `yaml:"item_attr"`
	} `yaml:"list"`
	Reviews struct {
		PageURLPattern string            `yaml:"page_url_pattern"` // "{restaurant_url}" 或模板
		ReviewItem     string            `yaml:"review_item"`
		Fields         map[string]string `yaml:"fields"` // restaurant/user/rating_text/rating_attr/content/date/permalink_attr
		NextPage       string            `yaml:"next_page"`
		MaxPages       int               `yaml:"max_pages"`
	} `yaml:"reviews"`
	// 渲染相关（仅 chromedp 有效）
	Render struct {
		Scroll ScrollConfig `yaml:"scroll"`
	} `yaml:"render"`
}

type ScrollConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Steps          int    `yaml:"steps"`             // 下拉次数上限
	PauseMS        int    `yaml:"pause_ms"`          // 每次下拉后等待（毫秒）
	WaitSelector   string `yaml:"wait_selector"`     // 每步滚动后等待出现的元素（可空）
	StopIfNoGrowth int    `yaml:"stop_if_no_growth"` // 连续几次高度无增长则提前停止
}

/* =========================
   数据模型
========================= */

type Review struct {
	Platform      string `json:"platform"`
	Keyword       string `json:"keyword"`
	Restaurant    string `json:"restaurant"`
	User          string `json:"user"`
	Rating        string `json:"rating"`
	Content       string `json:"content"`
	Date          string `json:"date"`
	Permalink     string `json:"permalink"`
	RestaurantURL string `json:"restaurant_url"`
	CapturedAtISO string `json:"captured_at"`
}

/* =========================
   工具函数
========================= */

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Global.TimeoutSec <= 0 {
		cfg.Global.TimeoutSec = 15
	}
	if cfg.Global.RatePerSec <= 0 {
		cfg.Global.RatePerSec = 0.8
	}
	return &cfg, nil
}

func substituteKeyword(u, keyword string) string {
	return strings.ReplaceAll(u, "{keyword}", url.QueryEscape(keyword))
}

func joinURL(base string, href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	u, err := url.Parse(base)
	if err != nil {
		return href
	}
	if strings.HasPrefix(href, "/") {
		u.Path = href
		return u.String()
	}
	u.Path = path.Join(path.Dir(u.Path), href)
	return u.String()
}

func domainAllowed(u string, allowed []string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := pu.Hostname()
	for _, d := range allowed {
		if strings.HasSuffix(host, d) {
			return true
		}
	}
	return false
}

// 从一个评论容器内提取字段；支持 "selector@attr" 语法
func pick(doc *goquery.Selection, sel string) string {
	if sel == "" {
		return ""
	}
	if at := strings.Index(sel, "@"); at > 0 {
		selector := sel[:at]
		attr := sel[at+1:]
		if s := doc.Find(selector).First(); s.Length() > 0 {
			val, _ := s.Attr(attr)
			return strings.TrimSpace(val)
		}
		return ""
	}
	return strings.TrimSpace(doc.Find(sel).First().Text())
}

/* =========================
   Cookie 读入（兼容 EditThisCookie/DevTools JSON）
========================= */

type simpleCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain,omitempty"`
	Path   string `json:"path,omitempty"`
}

/* =========================
   抓取引擎：HTTP / Chromedp
========================= */

type Fetcher interface {
	Fetch(ctx context.Context, url string) (string, error)
	Close() error
}

type HTTPFetcher struct {
	client  *http.Client
	ua      string
	limiter *rate.Limiter
}

func NewHTTPFetcher(timeout time.Duration, ua string, rps float64) (*HTTPFetcher, error) {
	jar, _ := cookiejar.New(nil)
	return &HTTPFetcher{
		client:  &http.Client{Timeout: timeout, Jar: jar},
		ua:      ua,
		limiter: rate.NewLimiter(rate.Limit(rps), 1),
	}, nil
}

func (h *HTTPFetcher) SetCookiesForPlatform(cookieFile string) {
	if cookieFile == "" {
		return
	}
	cs := LoadCookiesFromFile(cookieFile)
	if len(cs) == 0 {
		return
	}
	for _, ck := range cs {
		domain := strings.TrimPrefix(ck.Domain, ".")
		if domain == "" {
			continue
		}
		u, err := url.Parse("https://" + domain)
		if err == nil {
			h.client.Jar.SetCookies(u, []*http.Cookie{ck})
		}
	}
}

func (h *HTTPFetcher) Fetch(ctx context.Context, u string) (string, error) {
	if err := h.limiter.Wait(ctx); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	if h.ua != "" {
		req.Header.Set("User-Agent", h.ua)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("http %d for %s", resp.StatusCode, u)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (h *HTTPFetcher) Close() error { return nil }

type ChromedpFetcher struct {
	ctx     context.Context
	cancel  context.CancelFunc
	limiter *rate.Limiter
	ua      string
	timeout time.Duration
}

func NewChromedpFetcher(timeout time.Duration, rps float64, ua string) (*ChromedpFetcher, error) {
	// 使用随机 UA 如果没有提供
	if ua == "" {
		ua = randomUA()
	}
	
	// 配置 Chrome 选项 - 反检测设置
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-accelerated-2d-canvas", true),
		chromedp.Flag("disable-accelerated-jpeg-decoding", true),
		chromedp.Flag("disable-accelerated-mjpeg-decode", true),
		chromedp.Flag("disable-accelerated-video-decode", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-component-extensions-with-background-pages", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("window-size", "1280,800"),
		chromedp.UserAgent(ua),
	)
	
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, _ := chromedp.NewContext(allocCtx)
	
	return &ChromedpFetcher{
		ctx:     ctx,
		cancel:  cancel,
		limiter: rate.NewLimiter(rate.Limit(rps), 1),
		ua:      ua,
		timeout: timeout,
	}, nil
}

func (c *ChromedpFetcher) LoadCookiesFromFile(cookieFile string) error {
	if cookieFile == "" {
		return nil
	}
	cs := LoadCookiesFromFile(cookieFile)
	if len(cs) == 0 {
		return nil
	}
	var tasks chromedp.Tasks
	tasks = append(tasks, network.Enable())
	for _, ck := range cs {
		domain := strings.TrimPrefix(ck.Domain, ".")
		if domain == "" {
			continue
		}
		tasks = append(tasks, network.SetCookie(ck.Name, ck.Value).
			WithDomain(domain).
			WithPath(func() string {
				if ck.Path != "" {
					return ck.Path
				}
				return "/"
			}()))
	}
	if c.ua != "" {
		tasks = append(tasks, emulation.SetUserAgentOverride(c.ua))
	}
	return chromedp.Run(c.ctx, tasks)
}

func (c *ChromedpFetcher) Fetch(ctx context.Context, u string) (string, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return "", err
	}
	var html string
	timeout := c.timeout
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}
	pctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	tasks := chromedp.Tasks{
		network.Enable(),
		func() chromedp.Action {
			if c.ua != "" {
				return emulation.SetUserAgentOverride(c.ua)
			}
			return chromedp.ActionFunc(func(ctx context.Context) error { return nil })
		}(),
		chromedp.Navigate(u),
		chromedp.Sleep(700 * time.Millisecond),
		chromedp.OuterHTML("html", &html),
	}
	if err := chromedp.Run(pctx, tasks); err != nil {
		return "", err
	}
	return html, nil
}

// 带滚动的抓取（适合小红书瀑布流）
func (c *ChromedpFetcher) FetchWithScroll(ctx context.Context, u string, sc ScrollConfig) (string, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return "", err
	}
	timeout := c.timeout
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}
	pctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	if err := chromedp.Run(pctx,
		network.Enable(),
		func() chromedp.Action {
			if c.ua != "" {
				return emulation.SetUserAgentOverride(c.ua)
			}
			return chromedp.ActionFunc(func(ctx context.Context) error { return nil })
		}(),
		chromedp.Navigate(u),
		chromedp.Sleep(2*time.Second),
		// 执行反检测脚本
		chromedp.Evaluate(AntiDetectScript(), nil),
		chromedp.Sleep(500*time.Millisecond),
		// 模拟人类鼠标移动
		HumanLikeBehavior(pctx),
	); err != nil {
		return "", err
	}

	steps := sc.Steps
	if steps <= 0 {
		steps = 10
	}
	pause := time.Duration(sc.PauseMS) * time.Millisecond
	if pause <= 0 {
		pause = 600 * time.Millisecond
	}
	stopNoGrowth := sc.StopIfNoGrowth
	if stopNoGrowth <= 0 {
		stopNoGrowth = 2
	}

	noGrowthStreak := 0
	var lastH, curH int64

	for i := 0; i < steps; i++ {
		if err := chromedp.Run(pctx, chromedp.Evaluate(`document.scrollingElement.scrollHeight`, &curH)); err != nil {
			return "", err
		}
		if curH == lastH {
			noGrowthStreak++
		} else {
			noGrowthStreak = 0
		}
		lastH = curH

		// 随机滚动幅度，更像人类
		scrollAmount := rand.Intn(800) + 600
		if err := chromedp.Run(pctx,
			chromedp.Evaluate(fmt.Sprintf(`window.scrollBy(0, %d);`, scrollAmount), nil),
		); err != nil {
			return "", err
		}

		if sc.WaitSelector != "" {
			_ = chromedp.Run(pctx, chromedp.WaitVisible(sc.WaitSelector, chromedp.ByQuery))
		}
		
		// 随机停顿时间
		actualPause := pause + time.Duration(rand.Intn(800))*time.Millisecond
		time.Sleep(actualPause)

		if err := chromedp.Run(pctx, chromedp.Evaluate(`document.scrollingElement.scrollHeight`, &curH)); err != nil {
			return "", err
		}
		if curH <= lastH {
			noGrowthStreak++
		} else {
			noGrowthStreak = 0
			lastH = curH
		}
		if noGrowthStreak >= stopNoGrowth {
			break
		}
	}

	var html string
	if err := chromedp.Run(pctx, chromedp.OuterHTML("html", &html)); err != nil {
		return "", err
	}
	return html, nil
}

func (c *ChromedpFetcher) Close() error { c.cancel(); return nil }

/* =========================
   爬虫主体
========================= */

type Crawler struct {
	cfg           *Config
	fetcher       map[string]Fetcher
	engine        string
	maxPages      int
	concurrency   int
	keywords      []string
	ciInsensitive bool
	outCSV        string
	outJSONL      string
}

func NewCrawler(cfg *Config, engine string, maxPages, concurrency int, keywords []string, ciInsensitive bool, out string) (*Crawler, error) {
	c := &Crawler{
		cfg:           cfg,
		fetcher:       make(map[string]Fetcher),
		engine:        engine,
		maxPages:      maxPages,
		concurrency:   concurrency,
		keywords:      keywords,
		ciInsensitive: ciInsensitive,
	}
	if out != "" {
		c.outCSV = out
		c.outJSONL = strings.TrimSuffix(out, ".csv") + ".jsonl"
	}
	timeout := time.Duration(cfg.Global.TimeoutSec) * time.Second
	httpF, err := NewHTTPFetcher(timeout, cfg.Global.UserAgent, cfg.Global.RatePerSec)
	if err != nil {
		return nil, err
	}
	c.fetcher["http"] = httpF
	chrf, err := NewChromedpFetcher(timeout, cfg.Global.RatePerSec, cfg.Global.UserAgent)
	if err == nil {
		c.fetcher["chromedp"] = chrf
	}
	return c, nil
}

func (c *Crawler) Close() {
	for _, f := range c.fetcher {
		_ = f.Close()
	}
}

func (c *Crawler) getFetcher(engine string) (Fetcher, error) {
	if engine == "" {
		engine = c.engine
	}
	f, ok := c.fetcher[engine]
	if !ok || f == nil {
		return nil, fmt.Errorf("engine %s not available", engine)
	}
	return f, nil
}

func (c *Crawler) keywordMatch(s string) bool {
	if s == "" || len(c.keywords) == 0 {
		return true
	}
	text := s
	if c.ciInsensitive {
		text = strings.ToLower(text)
	}
	for _, kw := range c.keywords {
		k := kw
		if c.ciInsensitive {
			k = strings.ToLower(k)
		}
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

func (c *Crawler) prepareCookiesForPlatform(pCfg PlatformConfig) {
	if hf, ok := c.fetcher["http"].(*HTTPFetcher); ok {
		hf.SetCookiesForPlatform(pCfg.CookieFile)
	}
	if cf, ok := c.fetcher["chromedp"].(*ChromedpFetcher); ok {
		_ = cf.LoadCookiesFromFile(pCfg.CookieFile)
	}
}

func (c *Crawler) validateCookie(ctx context.Context, fetcher Fetcher, pCfg PlatformConfig, platform string) {
	if len(pCfg.StartURLs) == 0 {
		return
	}
	testURL := substituteKeyword(pCfg.StartURLs[0], "test")
	html, err := fetcher.Fetch(ctx, testURL)
	if err != nil {
		log.Printf("[%s] cookie 验证请求失败：%v", platform, err)
		return
	}
	
	// 检测登录状态
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		log.Printf("[%s] 解析验证页面失败：%v", platform, err)
		return
	}
	
	// 小红书特殊检测
	if platform == "xhs" {
		// 检测登录弹窗或提示
		if strings.Contains(html, "登录") || strings.Contains(html, "登錄") ||
		   strings.Contains(html, "请登录") || strings.Contains(html, "手机登录") {
			log.Printf("[%s] ⚠️ Cookie 已失效或未登录，请重新导出 Cookie 到 %s", platform, pCfg.CookieFile)
			return
		}
		// 检测是否有笔记列表
		items := doc.Find("section.note-item").Length()
		if items == 0 {
			items = doc.Find("div.feed-item").Length()
		}
		if items == 0 {
			log.Printf("[%s] ⚠️ 未检测到笔记列表，可能被反爬拦截，请检查 Cookie 或降低频率", platform)
		} else {
			log.Printf("[%s] ✓ Cookie 验证通过，检测到 %d 个笔记项", platform, items)
		}
		return
	}
	
	// 通用登录检测
	if strings.Contains(html, "登录") || strings.Contains(html, "登錄") ||
	   strings.Contains(html, "请登录") || strings.Contains(html, "login") {
		log.Printf("[%s] ⚠️ Cookie 似乎失效或未登录，请更新 %s", platform, pCfg.CookieFile)
	}
}

/* ====== 核心抓取流程（平台） ====== */

func (c *Crawler) crawlPlatform(ctx context.Context, platform string, pCfg PlatformConfig, results chan<- Review, keyword string) error {
	engine := pCfg.Engine
	if engine == "" {
		engine = c.engine
	}
	fetcher, err := c.getFetcher(engine)
	if err != nil {
		return err
	}

	type queueItem struct {
		URL     string
		PageNo  int
		IsList  bool
		FromURL string
	}

	visited := sync.Map{}
	enqueue := func(u string, page int, isList bool, from string, q chan<- queueItem) {
		if _, loaded := visited.LoadOrStore(u, struct{}{}); loaded {
			return
		}
		q <- queueItem{URL: u, PageNo: page, IsList: isList, FromURL: from}
	}

	// 注入 Cookie + 轻量验证
	c.prepareCookiesForPlatform(pCfg)
	c.validateCookie(ctx, fetcher, pCfg, platform)

	// 起始
	q := make(chan queueItem, 128)
	for _, su := range pCfg.StartURLs {
		enqueue(substituteKeyword(su, keyword), 1, true, "", q)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	workers := c.concurrency
	if workers < 1 {
		workers = 1
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range q {
				if !domainAllowed(it.URL, pCfg.AllowedDomains) {
					continue
				}

				var html string
				var err error
				// 仅在 chromedp 且启用滚动时，在“评论页/详情页”(IsList=false)使用滚动抓取
				if cf, ok := fetcher.(*ChromedpFetcher); ok && pCfg.Render.Scroll.Enabled && !it.IsList {
					html, err = cf.FetchWithScroll(ctx, it.URL, pCfg.Render.Scroll)
				} else {
					html, err = fetcher.Fetch(ctx, it.URL)
				}
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					continue
				}
				doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					continue
				}

				if it.IsList {
					// 列表页抽链接
					if ssel, attr := pCfg.List.ItemSelector, pCfg.List.ItemAttr; ssel != "" && attr != "" {
						doc.Find(ssel).Each(func(_ int, s *goquery.Selection) {
							link, _ := s.Attr(attr)
							if link == "" {
								return
							}
							restURL := joinURL(it.URL, link)
							reviewPage := strings.ReplaceAll(pCfg.Reviews.PageURLPattern, "{restaurant_url}", restURL)
							enqueue(reviewPage, 1, false, restURL, q)
						})
					}
					// 列表翻页
					if (pCfg.Reviews.MaxPages <= 0 || it.PageNo < pCfg.Reviews.MaxPages) && pCfg.Reviews.NextPage != "" {
						if n := doc.Find(pCfg.Reviews.NextPage).First(); n.Length() > 0 {
							if href, ok := n.Attr("href"); ok {
								enqueue(joinURL(it.URL, href), it.PageNo+1, true, "", q)
							}
						}
					}
				} else {
					// 评论解析
					itemSel := pCfg.Reviews.ReviewItem
					if itemSel == "" {
						continue
					}
					doc.Find(itemSel).Each(func(_ int, s *goquery.Selection) {
						r := Review{
							Platform:      platform,
							Keyword:       keyword,
							CapturedAtISO: time.Now().Format(time.RFC3339),
							RestaurantURL: it.FromURL,
						}
						for name, selector := range pCfg.Reviews.Fields {
							val := pick(s, selector)
							switch name {
							case "restaurant":
								r.Restaurant = val
							case "user":
								r.User = val
							case "rating_text", "rating_attr":
								if r.Rating == "" {
									r.Rating = val
								}
							case "content":
								r.Content = strings.TrimSpace(val)
							case "date":
								r.Date = val
							case "permalink_attr":
								r.Permalink = val
							}
						}
						if !c.keywordMatch(r.Content) && !c.keywordMatch(r.Restaurant) {
							return
						}
						results <- r
					})
					// 评论翻页（如果评论页也有 next）
					if (pCfg.Reviews.MaxPages <= 0 || it.PageNo < pCfg.Reviews.MaxPages) && pCfg.Reviews.NextPage != "" {
						if n := doc.Find(pCfg.Reviews.NextPage).First(); n.Length() > 0 {
							if href, ok := n.Attr("href"); ok {
								enqueue(joinURL(it.URL, href), it.PageNo+1, false, it.FromURL, q)
							}
						}
					}
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	var firstErr error
	for e := range errCh {
		if firstErr == nil {
			firstErr = e
		}
	}
	return firstErr
}

/* =========================
   输出
========================= */

func writeCSV(path string, rows [][]string) error {
	if dir := filepath.Dir(filepath.Clean(path)); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	return w.Error()
}

/* =========================
   主函数
========================= */

func main() {
	log.Println("🚀 评论抓取工具启动")
	log.Printf("🖥️  操作系统: %s", runtime.GOOS)
	log.Printf("🌐 使用 User-Agent: %s", randomUA())
	
	var (
		cfgPath          = flag.String("config", "config.yml", "config yaml path")
		keywordsStr      = flag.String("keywords", "", "comma-separated keywords, e.g. 生蚝,蟹粉")
		platforms        = flag.String("platforms", "dianping,xhs", "comma-separated platforms defined in config")
		engine           = flag.String("engine", "http", "default engine: http|chromedp")
		maxPages         = flag.Int("maxPages", 5, "max pages per list/review")
		concurrency      = flag.Int("concurrency", 2, "concurrency per platform")
		caseInsensitive  = flag.Bool("ci", true, "case-insensitive keyword match")
		outPath          = flag.String("out", "reviews.csv", "output CSV path; .jsonl will also be generated")
		interactiveLogin = flag.Bool("login", false, "interactive login mode for xiaohongshu")
	)
	flag.Parse()

	if *keywordsStr == "" {
		log.Fatal("❌ 请用 -keywords 指定关键词，多个用逗号分隔")
	}
	kw := strings.Split(*keywordsStr, ",")
	for i := range kw {
		kw[i] = strings.TrimSpace(kw[i])
	}
	log.Printf("📋 关键词: %v", kw)

	// 如果启用交互式登录模式
	if *interactiveLogin {
		log.Println("🔐 启用交互式登录模式")
		if err := runInteractiveLogin(*platforms); err != nil {
			log.Fatalf("❌ 登录失败: %v", err)
		}
		return
	}

	cfg, err := loadConfig(*cfgPath)
	must(err)
	log.Printf("⚙️ 配置加载成功，全局限速: %.1f req/s", cfg.Global.RatePerSec)

	crawler, err := NewCrawler(cfg, *engine, *maxPages, *concurrency, kw, *caseInsensitive, *outPath)
	must(err)
	defer crawler.Close()

	plats := strings.Split(*platforms, ",")
	for i := range plats {
		plats[i] = strings.TrimSpace(plats[i])
	}
	log.Printf("🎯 目标平台: %v", plats)

	// 针对小红书检查 Cookie，如果无效且是 chromedp 引擎，尝试交互式登录
	for _, p := range plats {
		if p == "xhs" && *engine == "chromedp" {
			if !checkXHSCookieValid() {
				log.Println("⚠️ 小红书 Cookie 无效，启动交互式登录...")
				log.Println("💡 提示: 使用 -login 参数可以提前登录")
				// 这里我们会在 Crawler 中处理登录逻辑
			}
		}
	}

	ctx := context.Background()
	results := make(chan Review, 1024)
	var wg sync.WaitGroup

	for _, p := range plats {
		pcfg, ok := cfg.Platforms[p]
		if !ok {
			log.Printf("平台未在配置中找到: %s，跳过", p)
			continue
		}
		for _, k := range kw {
			wg.Add(1)
			go func(platform string, pc PlatformConfig, keyword string) {
				defer wg.Done()
				if err := crawler.crawlPlatform(ctx, platform, pc, results, keyword); err != nil {
					log.Printf("[%s][%s] error: %v", platform, keyword, err)
				}
			}(p, pcfg, k)
		}
	}

	csvRows := [][]string{{"platform", "keyword", "restaurant", "user", "rating", "content", "date", "permalink", "restaurant_url", "captured_at"}}
	jsonl, err := os.Create(strings.TrimSuffix(*outPath, ".csv") + ".jsonl")
	must(err)
	defer jsonl.Close()

	go func() {
		wg.Wait()
		close(results)
	}()

	reSpace := regexp.MustCompile(`\s+`)
	for r := range results {
		csvRows = append(csvRows, []string{
			r.Platform, r.Keyword, r.Restaurant, r.User, r.Rating,
			reSpace.ReplaceAllString(strings.TrimSpace(r.Content), " "),
			r.Date, r.Permalink, r.RestaurantURL, r.CapturedAtISO,
		})
		enc := json.NewEncoder(jsonl)
		_ = enc.Encode(r)
	}
	must(writeCSV(*outPath, csvRows))
	log.Printf("完成：写出 %d 条；CSV: %s；JSONL: %s", len(csvRows)-1, *outPath, strings.TrimSuffix(*outPath, ".csv")+".jsonl")
}

// checkXHSCookieValid 检查小红书 Cookie 是否有效
func checkXHSCookieValid() bool {
	cookieFile := "cookies/xhs.json"
	data, err := os.ReadFile(cookieFile)
	if err != nil {
		return false
	}
	
	// 简单检查是否包含 session 相关的 cookie
	content := string(data)
	sessionIndicators := []string{"web_session", "session", "ticket", "token", "login"}
	for _, indicator := range sessionIndicators {
		if contains(content, indicator) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// runInteractiveLogin 运行交互式登录
func runInteractiveLogin(platforms string) error {
	plats := strings.Split(platforms, ",")
	
	for _, p := range plats {
		p = strings.TrimSpace(p)
		if p != "xhs" {
			log.Printf("跳过 %s，交互式登录仅支持 xhs", p)
			continue
		}
		
		log.Println("🔐 启动小红书交互式登录...")
		log.Println("💡 将打开浏览器，请在浏览器中完成登录")
		log.Println("   推荐使用：手机号 + 验证码登录")
		
		// 创建 chromedp 上下文（有头模式）
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", false),
			chromedp.Flag("window-size", "1280,800"),
			chromedp.UserAgent(randomUA()),
		)
		
		allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
		defer cancel()
		
		ctx, cancel := chromedp.NewContext(allocCtx)
		defer cancel()
		
		// 访问小红书并等待登录
		if err := chromedp.Run(ctx,
			chromedp.Navigate("https://www.xiaohongshu.com"),
			chromedp.Sleep(2*time.Second),
		); err != nil {
			return err
		}
		
		log.Println("⏳ 请在浏览器中完成登录，然后按回车键继续...")
		fmt.Scanln()
		
		// 获取 Cookie
		var cookies []chromedp.Cookie
		if err := chromedp.Run(ctx, chromedp.Cookies(&cookies)); err != nil {
			return fmt.Errorf("获取 cookie 失败: %w", err)
		}
		
		// 保存 Cookie
		cookieFile := "cookies/xhs.json"
		os.MkdirAll("cookies", 0755)
		
		formattedCookies := make([]map[string]interface{}, 0, len(cookies))
		for _, c := range cookies {
			if c.Domain == "" || !strings.Contains(c.Domain, "xiaohongshu") {
				continue
			}
			formattedCookies = append(formattedCookies, map[string]interface{}{
				"name":     c.Name,
				"value":    c.Value,
				"domain":   c.Domain,
				"path":     c.Path,
				"httpOnly": c.HTTPOnly,
				"secure":   c.Secure,
				"sameSite": "Lax",
			})
		}
		
		data, err := json.MarshalIndent(formattedCookies, "", "  ")
		if err != nil {
			return err
		}
		
		if err := os.WriteFile(cookieFile, data, 0644); err != nil {
			return err
		}
		
		log.Printf("✅ Cookie 已保存到 %s", cookieFile)
		log.Printf("📊 共导出 %d 个 Cookie", len(formattedCookies))
		
		// 验证登录
		if err := chromedp.Run(ctx,
			chromedp.Navigate("https://www.xiaohongshu.com/search_result?keyword=test"),
			chromedp.Sleep(2*time.Second),
		); err != nil {
			return err
		}
		
		var html string
		if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html)); err != nil {
			return err
		}
		
		if !strings.Contains(html, "登录") || !strings.Contains(html, "登录后查看") {
			log.Println("✅ 登录验证通过！")
		} else {
			log.Println("⚠️  可能未登录成功，请重新运行")
		}
	}
	
	return nil
}

/*
运行示例：
go run . \
  -keywords "生蚝,蟹粉,牛排" \
  -platforms "dianping,xhs" \
  -engine chromedp \
  -maxPages 3 \
  -concurrency 2 \
  -out reviews.csv

说明：
1) 在 config.yml 中仅配置 dianping/xhs；为平台准备 cookies/{platform}.json（浏览器导出）。
2) 遵守平台条款与 robots.txt；仅抓取公开可访问页面，不要绕过验证码/签名/登录墙等技术措施。
3) 小红书瀑布流：在 xhs 平台 Render.Scroll.enabled=true，并设置 steps/pause_ms/wait_selector。
4) 小红书登录：使用 -login 参数启动交互式登录模式
*/

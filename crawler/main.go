package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/emulation"
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
		PageURLPattern string            `yaml:"page_url_pattern"` // "{restaurant_url}"
		ReviewItem     string            `yaml:"review_item"`
		Fields         map[string]string `yaml:"fields"` // restaurant/user/rating_text/rating_attr/content/date/permalink_attr
		NextPage       string            `yaml:"next_page"`
		MaxPages       int               `yaml:"max_pages"`
	} `yaml:"reviews"`
	Render struct {
		Scroll ScrollConfig `yaml:"scroll"`
	} `yaml:"render"`
}

type ScrollConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Steps          int    `yaml:"steps"`
	PauseMS        int    `yaml:"pause_ms"`
	WaitSelector   string `yaml:"wait_selector"`
	StopIfNoGrowth int    `yaml:"stop_if_no_growth"`
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
	if cfg.Global.UserAgent == "" {
		// 给个保底 UA，建议在 config.yml 里显式配置为你浏览器的 UA
		cfg.Global.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
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

// 从容器内提取字段；支持 "selector@attr"
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
   Cookie 读入（兼容扩展/DevTools JSON）
========================= */

type simpleCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain,omitempty"`
	Path   string `json:"path,omitempty"`
}

func LoadCookiesFromFile(path string) []*http.Cookie {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var raw []simpleCookie
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return nil
	}
	var cookies []*http.Cookie
	for _, c := range raw {
		ck := &http.Cookie{
			Name:  c.Name,
			Value: c.Value,
			Path:  "/",
		}
		if c.Domain != "" {
			ck.Domain = c.Domain
		}
		if c.Path != "" {
			ck.Path = c.Path
		}
		cookies = append(cookies, ck)
	}
	return cookies
}

/* =========================
   抓取引擎：HTTP / Chromedp
========================= */

type Fetcher interface {
	Fetch(ctx context.Context, url string) (string, error)
	Close() error
}

/*** HTTP fetcher + debug ***/

type dbgRoundTripper struct {
	rt    http.RoundTripper
	debug bool
}

func (d dbgRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if d.debug {
		log.Printf("[HTTP dbg] -> %s %s", req.Method, req.URL.String())
		log.Printf("[HTTP dbg] UA: %s", req.Header.Get("User-Agent"))
		if ck := req.Header.Get("Cookie"); ck != "" {
			log.Printf("[HTTP dbg] Cookie: %s", ck)
		}
	}
	resp, err := d.rt.RoundTrip(req)
	if d.debug && resp != nil {
		log.Printf("[HTTP dbg] <- %d %s", resp.StatusCode, resp.Request.URL.String())
		if loc := resp.Header.Get("Location"); loc != "" {
			log.Printf("[HTTP dbg] Redirect: %s", loc)
		}
	}
	return resp, err
}

type HTTPFetcher struct {
	client  *http.Client
	ua      string
	limiter *rate.Limiter
}

func NewHTTPFetcher(timeout time.Duration, ua string, rps float64) (*HTTPFetcher, error) {
	jar, _ := cookiejar.New(nil)
	base := http.DefaultTransport
	client := &http.Client{
		Timeout:   timeout,
		Jar:       jar,
		Transport: dbgRoundTripper{rt: base, debug: false},
	}
	return &HTTPFetcher{
		client:  client,
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
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		// 允许 3xx，在外层自检会特殊处理
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			b, _ := io.ReadAll(resp.Body)
			return string(b), nil
		}
		return "", fmt.Errorf("http %d for %s", resp.StatusCode, u)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (h *HTTPFetcher) Close() error { return nil }

/*** Chromedp fetcher + debug ***/

type ChromedpFetcher struct {
	ctx     context.Context
	cancel  context.CancelFunc
	limiter *rate.Limiter
	ua      string
	timeout time.Duration
	debug   bool
}

func NewChromedpFetcher(timeout time.Duration, rps float64, ua string) (*ChromedpFetcher, error) {
	// ExecAllocator：规避 AutomationControlled，设置语言/窗口等
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true), // 调试可改为 false
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("lang", "zh-CN,zh;q=0.9,en;q=0.8"),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("window-size", "1366,768"),
	)
	actx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(actx)

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
	return chromedp.Run(c.ctx, tasks)
}

func extraHeaders(ua string, referer string) chromedp.Action {
	h := network.Headers{
		"User-Agent":                ua,
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language":           "zh-CN,zh;q=0.9,en;q=0.8",
		"Connection":                "keep-alive",
		"Upgrade-Insecure-Requests": "1",
		"Referer":                   referer,
	}
	return network.SetExtraHTTPHeaders(h)
}

func stealthOnNewDocument() chromedp.Action {
	script := `(function(){
        Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
        window.chrome = window.chrome || { runtime: {} };
        const originalQuery = window.navigator.permissions && window.navigator.permissions.query;
        if (originalQuery) {
            window.navigator.permissions.query = (parameters) => (
                parameters.name === 'notifications' ?
                Promise.resolve({ state: Notification.permission }) :
                originalQuery(parameters)
            );
        }
        Object.defineProperty(navigator, 'plugins', { get: () => [1,2,3] });
        Object.defineProperty(navigator, 'languages', { get: () => ['zh-CN','zh','en'] });
    })();`
	return chromedp.EvaluateOnNewDocument(script)
}

func (c *ChromedpFetcher) Fetch(ctx context.Context, u string) (string, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return "", err
	}
	timeout := c.timeout
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}
	pctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	if c.debug {
		chromedp.ListenTarget(pctx, func(ev interface{}) {
			switch e := ev.(type) {
			case *network.EventRequestWillBeSent:
				if e.Request != nil {
					log.Printf("[CDP dbg] -> %s %s", e.Request.Method, e.Request.URL)
					if ua, ok := e.Request.Headers["User-Agent"]; ok {
						log.Printf("[CDP dbg] UA: %v", ua)
					}
					if ck, ok := e.Request.Headers["Cookie"]; ok {
						log.Printf("[CDP dbg] Cookie: %v", ck)
					}
				}
			case *network.EventResponseReceived:
				if e.Response != nil {
					log.Printf("[CDP dbg] <- %d %s", int(e.Response.Status), e.Response.URL)
					if loc, ok := e.Response.Headers["Location"]; ok {
						log.Printf("[CDP dbg] Redirect: %v", loc)
					}
				}
			}
		})
	}

	var html string
	tasks := chromedp.Tasks{
		network.Enable(),
		emulation.SetUserAgentOverride(c.ua),
		extraHeaders(c.ua, "https://www.xiaohongshu.com/"),
		stealthOnNewDocument(),
		// 热身首页，建立会话
		chromedp.Navigate("https://www.xiaohongshu.com/"),
		chromedp.Sleep(400 * time.Millisecond),

		// 目标页
		chromedp.Navigate(u),
		chromedp.Sleep(700 * time.Millisecond),
		chromedp.OuterHTML("html", &html),
	}
	if err := chromedp.Run(pctx, tasks); err != nil {
		return "", err
	}
	return html, nil
}

// 带滚动
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

	if c.debug {
		chromedp.ListenTarget(pctx, func(ev interface{}) {
			switch e := ev.(type) {
			case *network.EventRequestWillBeSent:
				if e.Request != nil {
					log.Printf("[CDP dbg] -> %s %s", e.Request.Method, e.Request.URL)
					if ua, ok := e.Request.Headers["User-Agent"]; ok {
						log.Printf("[CDP dbg] UA: %v", ua)
					}
					if ck, ok := e.Request.Headers["Cookie"]; ok {
						log.Printf("[CDP dbg] Cookie: %v", ck)
					}
				}
			case *network.EventResponseReceived:
				if e.Response != nil {
					log.Printf("[CDP dbg] <- %d %s", int(e.Response.Status), e.Response.URL)
					if loc, ok := e.Response.Headers["Location"]; ok {
						log.Printf("[CDP dbg] Redirect: %v", loc)
					}
				}
			}
		})
	}

	if err := chromedp.Run(pctx,
		network.Enable(),
		emulation.SetUserAgentOverride(c.ua),
		extraHeaders(c.ua, "https://www.xiaohongshu.com/"),
		stealthOnNewDocument(),
		// 热身首页
		chromedp.Navigate("https://www.xiaohongshu.com/"),
		chromedp.Sleep(300*time.Millisecond),

		chromedp.Navigate(u),
		chromedp.Sleep(500*time.Millisecond),
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

		if err := chromedp.Run(pctx, chromedp.Evaluate(`window.scrollTo(0, document.scrollingElement.scrollHeight);`, nil)); err != nil {
			return "", err
		}
		if sc.WaitSelector != "" {
			_ = chromedp.Run(pctx, chromedp.WaitVisible(sc.WaitSelector, chromedp.ByQuery))
		}
		time.Sleep(pause)

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
   调试 & 自检
========================= */

func dumpJarCookies(jar http.CookieJar, rawURL string) {
	u, _ := url.Parse(rawURL)
	if u == nil || jar == nil {
		log.Println("[debug] dumpJarCookies: invalid")
		return
	}
	cs := jar.Cookies(u)
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}
	log.Printf("[debug] cookies for %s -> %s", u.Host, strings.Join(parts, "; "))
}

func looksLikeLoginPage(html, finalURL string) bool {
	h := strings.ToLower(html)
	if strings.Contains(strings.ToLower(finalURL), "/login") {
		return true
	}
	bad := []string{"验证码", "请登录", "账号登录", "密码登录", "滑块验证", "sms", "captcha", "geetest"}
	hits := 0
	for _, kw := range bad {
		if strings.Contains(h, kw) {
			hits++
		}
	}
	return hits >= 2
}

func pageBlocked(html string) bool {
	h := strings.ToLower(html)
	return strings.Contains(h, "redcaptcha") || strings.Contains(h, "滑块验证") ||
		strings.Contains(h, "验证码") || strings.Contains(h, "geetest")
}

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
	debug         bool
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
		if c.debug && len(pCfg.StartURLs) > 0 {
			dumpJarCookies(hf.client.Jar, pCfg.StartURLs[0])
		}
	}
	if cf, ok := c.fetcher["chromedp"].(*ChromedpFetcher); ok {
		_ = cf.LoadCookiesFromFile(pCfg.CookieFile)
		cf.debug = c.debug
	}
}

// 自检：允许 301/302，只有重定向到 /login 才判定失效
func (c *Crawler) validateCookie(ctx context.Context, fetcher Fetcher, pCfg PlatformConfig, platform string) {
	if len(pCfg.StartURLs) == 0 {
		return
	}
	start := pCfg.StartURLs[0]

	if hf, ok := fetcher.(*HTTPFetcher); ok {
		tmp := *hf.client // 复制 client
		tmp.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		req, _ := http.NewRequestWithContext(ctx, "GET", start, nil)
		if c.cfg.Global.UserAgent != "" {
			req.Header.Set("User-Agent", c.cfg.Global.UserAgent)
		}
		resp, err := tmp.Do(req)
		if err != nil {
			log.Printf("[%s] cookie/self-check fail: %v", platform, err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			if strings.Contains(strings.ToLower(loc), "/login") {
				log.Printf("[%s] Cookie 失效：重定向到登录页 %s", platform, loc)
				return
			}
			log.Printf("[%s] cookie/self-check: %d redirect to %s (OK)", platform, resp.StatusCode, loc)
			return
		}

		b, _ := io.ReadAll(resp.Body)
		if looksLikeLoginPage(string(b), resp.Request.URL.String()) {
			log.Printf("[%s] Cookie 疑似失效：页面出现登录/验证码元素。", platform)
		} else {
			log.Printf("[%s] cookie/self-check OK：未检测到登录提示。", platform)
		}
		return
	}

	// chromedp 分支：直接抓页面
	html, err := fetcher.Fetch(ctx, start)
	if err != nil {
		log.Printf("[%s] cookie/self-check fetch fail: %v", platform, err)
		return
	}
	if looksLikeLoginPage(html, start) {
		log.Printf("[%s] Cookie 疑似失效：页面出现登录/验证码元素。", platform)
	} else {
		log.Printf("[%s] cookie/self-check OK：未检测到登录提示。", platform)
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

	// 注入 Cookie + 自检
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
				// 评论页/详情页用滚动（若启用）
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

				// 被验证码/登录页拦截，记录样本便于调试
				if pageBlocked(html) {
					_ = os.MkdirAll("debug", 0755)
					fn := fmt.Sprintf("debug/%s_blocked_%d.html", platform, time.Now().UnixNano())
					_ = os.WriteFile(fn, []byte(html), 0644)
					log.Printf("[%s] WARN: page blocked (captcha/login). dumped: %s", platform, fn)
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
					// 列表抽取链接
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
					// 评论翻页
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
	var (
		cfgPath         = flag.String("config", "config.yml", "config yaml path")
		keywordsStr     = flag.String("keywords", "", "comma-separated keywords, e.g. 生蚝,海鲜")
		platforms       = flag.String("platforms", "xhs", "comma-separated platforms defined in config")
		engine          = flag.String("engine", "chromedp", "default engine: http|chromedp")
		maxPages        = flag.Int("maxPages", 5, "max pages per list/review")
		concurrency     = flag.Int("concurrency", 2, "concurrency per platform")
		caseInsensitive = flag.Bool("ci", true, "case-insensitive keyword match")
		outPath         = flag.String("out", "reviews.csv", "output CSV path; .jsonl will also be generated")
		debugFlag       = flag.Bool("debug", false, "enable verbose cookie/UA/redirect debug log")
		uaFlag          = flag.String("ua", "", "override user-agent")
	)
	flag.Parse()

	if *keywordsStr == "" {
		log.Fatal("请用 -keywords 指定关键词，多个用逗号分隔")
	}
	kw := strings.Split(*keywordsStr, ",")
	for i := range kw {
		kw[i] = strings.TrimSpace(kw[i])
	}

	cfg, err := loadConfig(*cfgPath)
	must(err)
	if *uaFlag != "" {
		cfg.Global.UserAgent = *uaFlag
	}

	crawler, err := NewCrawler(cfg, *engine, *maxPages, *concurrency, kw, *caseInsensitive, *outPath)
	must(err)
	defer crawler.Close()
	crawler.debug = *debugFlag

	// 打开 HTTP debug
	if *debugFlag {
		if hf, ok := crawler.fetcher["http"].(*HTTPFetcher); ok {
			if t, ok := hf.client.Transport.(dbgRoundTripper); ok {
				t.debug = true
				hf.client.Transport = t
			}
		}
		// Chromedp debug 在 fetcher 内部通过 cf.debug 控制
	}

	plats := strings.Split(*platforms, ",")
	for i := range plats {
		plats[i] = strings.TrimSpace(plats[i])
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

/*
用法示例（小红书 + 滚动）：
go run . \
  -config config.yml \
  -keywords "海鲜" \
  -platforms "xhs" \
  -engine chromedp \
  -maxPages 3 \
  -concurrency 2 \
  -out reviews.csv \
  -debug

注意：
1) config.yml 的 xhs.start_urls 推荐使用：
   https://www.xiaohongshu.com/explore?keyword={keyword}
2) cookies/xhs.json 中 acw_tc 通常为 hostOnly（domain=www.xiaohongshu.com），其余用 .xiaohongshu.com。
3) 仅当重定向到 /login 才视为失效；正常 301/302（如到 /explore）视为 OK。
4) 全局 UA（global.user_agent）与导出 cookies 的浏览器 UA 保持一致；也可用 -ua 临时覆盖。
*/

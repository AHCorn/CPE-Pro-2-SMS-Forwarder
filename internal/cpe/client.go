// Package cpe 封装与烽火 5G CPE 路由器 HTTP API 的交互，包括登录、短信获取和会话保活。
//
// 认证协议（与前端 polyfill.js 的实现保持一致）：
//
//  1. GET /fh_api/tmp/FHNCAPIS?ajaxmethod=is_encrypt
//     响应形如 {"data":"<base64>"}。前 6 字节为服务端随机混淆前缀，剩余部分
//     是 RSA-PKCS1-v1.5 密文；用 polyfill.js 硬编码的私钥解密后得到 magic
//     字符串（本固件上观察为 "ydbVj"，为兼容未来固件变更，运行时动态解密）。
//
//  2. GET /fh_api/tmp/FHNCAPIS?ajaxmethod=get_refresh_sessionid
//     返回 {"sessionid":"<32 字符>"}。
//
//  3. aesKey = crypto.DeriveAESKey(sessionid, magic)
//     复原 polyfill.js 中 _0x46c520_ 函数的派生算法。
//
//  4. POST /fh_api/sign/DO_WEB_LOGIN?_<rand>
//     Content-Type: application/json; charset=utf-8
//     Body = <6 个 base36 随机字符> + hex(AES-128-CBC(plaintext, aesKey, FixedIV))
//     其中 plaintext 是固定插入顺序的 JSON：
//     {"dataObj":{"username":"..","password":".."},
//     "ajaxmethod":"DO_WEB_LOGIN","sessionid":"<完整 sid>"}
//     登录响应为明文 JSON {"result":0,"user":1}。
//
//  5. 其他加密接口（如 get_sms_data、heartbeat 之外的业务方法）：
//     POST /fh_api/tmp/FHAPIS?_<rand>
//     Content-Type: application/json; charset=utf-8
//     Body 结构与登录相同，只是 ajaxmethod 换成目标方法名。
//     响应若为 hex 密文则自动解密，否则按明文返回。
//
// 烽火固件将会话绑定到 TCP 连接 + 客户端 IP（不是标准 Cookie 会话），因此必须
// 复用同一条 keep-alive 连接，NewClient 的 Transport 已设置 MaxConnsPerHost=1。
package cpe

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/crypto"
)

// maxResponseBytes 限制单次读取路由器响应的字节数。与路由器走的是 LAN 明文 HTTP，
// 异常固件或同网段中间人可能返回超大响应；不设上限的 io.ReadAll 会被诱导耗尽内存（DoS）。
// 短信列表通常仅 KB 级，8 MiB 远超正常上限，超过即视为异常并中止本次读取。
const maxResponseBytes = 8 << 20

// SMSSession 代表一个短信对话（一个联系人的所有消息）
type SMSSession struct {
	Phone    string   `json:"session_phone"`
	Messages []SMSMsg `json:"-"`
}

// SMSMsg 代表一条短信
type SMSMsg struct {
	RcvOrSend string `json:"rcvorsend"`
	KeyIndex  string `json:"key_index"`
	Time      string `json:"time"`
	IsOpened  string `json:"isOpened"`
	Content   string `json:"msg_content"`
	ChildNode string `json:"childnode"`
}

// Client 封装与 CPE 路由器的 HTTP API 交互。
//
// 浏览器行为：is_encrypt 在登录时调用一次，得到的 magic 字符串全会话复用；
// 但 get_refresh_sessionid 在每次加密请求前都要重新获取一次，拿到新的 sid
// 并据此派生新的 AES key。因此 Client 缓存 magic，每次加密请求都 refresh sid。
type Client struct {
	host     string
	username string
	password string
	http     *http.Client
	magic    string // RSA 解密 is_encrypt 得到的 KDF magic 值，登录后缓存复用

	localMu sync.Mutex
	localIP string // 到路由器的 TCP 连接所用本地源 IP，首次成功拨号后由 DialContext 记录
}

func NewClient(host, username, password string) *Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	c := &Client{host: host, username: username, password: password}
	transport := &http.Transport{
		// 包一层 DialContext 以记录本地源 IP：与路由器同处一个 LAN（无 NAT），
		// 该 IP 即路由器在 other_logged 中看到的 login_ip，可据此识别"占用者其实是自己"。
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err == nil {
				if ta, ok := conn.LocalAddr().(*net.TCPAddr); ok {
					c.setLocalIP(ta.IP.String())
				}
			}
			return conn, err
		},
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		MaxConnsPerHost:     1,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		DisableKeepAlives:   false,
	}
	jar, _ := cookiejar.New(nil)
	c.http = &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		Jar:       jar,
	}
	return c
}

func (c *Client) setLocalIP(ip string) {
	c.localMu.Lock()
	c.localIP = ip
	c.localMu.Unlock()
}

// LocalIP 返回本客户端到路由器的 TCP 连接所用本地源 IP（首次成功拨号后可用，未拨号时为空）。
// 用于与 OtherLogged 返回的持有者 IP 比较，区分"会话被他人占用"与"自身陈旧会话/定期重登"。
func (c *Client) LocalIP() string {
	c.localMu.Lock()
	defer c.localMu.Unlock()
	return c.localIP
}

func (c *Client) baseURL() string {
	return "http://" + c.host
}

// debugLogin 控制是否打印详细登录调试信息，仅在本地排障时开启
var debugLogin = os.Getenv("CPE_DEBUG") == "1"

// doRequest 发送 HTTP 请求，Cookie 由 client.Jar 自动管理。
// referer 传相对路径（如 "login.html" 或 "main.html"），为空则不设置。
// contentType 仅在 POST 时生效，为空则默认 application/json; charset=utf-8。
func (c *Client) doRequest(method, path, body, referer, contentType string) (int, string, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, c.baseURL()+path, reader)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", c.baseURL()+"/"+referer)
	}
	if method == "POST" && body != "" {
		ct := contentType
		if ct == "" {
			ct = "application/json; charset=utf-8"
		}
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Origin", c.baseURL())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	// 多读 1 字节用于判定是否超限：读到 maxResponseBytes+1 即说明响应超过上限。
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, "", err
	}
	if int64(len(b)) > maxResponseBytes {
		return resp.StatusCode, "", fmt.Errorf("响应体超过 %d 字节上限，疑似异常响应，已中止读取", maxResponseBytes)
	}
	return resp.StatusCode, string(b), nil
}

// fetchMagic 调用 is_encrypt 并 RSA 解密出 KDF 所需的 magic 字符串
func (c *Client) fetchMagic() (string, error) {
	status, body, err := c.doRequest("GET",
		"/fh_api/tmp/FHNCAPIS?ajaxmethod=is_encrypt", "", "login.html", "")
	if err != nil {
		return "", fmt.Errorf("请求 is_encrypt: %w", err)
	}
	if status != 200 {
		return "", fmt.Errorf("is_encrypt 返回 status=%d", status)
	}
	var resp struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return "", fmt.Errorf("解析 is_encrypt (body=%s): %w", truncate(body, 80), err)
	}
	if resp.Data == "" {
		return "", fmt.Errorf("is_encrypt data 字段为空")
	}
	magic, err := crypto.RSADecryptIsEncrypt(resp.Data)
	if err != nil {
		return "", fmt.Errorf("RSA 解密 is_encrypt: %w", err)
	}
	if magic == "" {
		return "", fmt.Errorf("RSA 解密结果为空")
	}
	if debugLogin {
		log.Printf("[DEBUG] is_encrypt magic=%q", magic)
	}
	return magic, nil
}

// fetchSessionID 获取路由器下发的 32 字符 sessionid
func (c *Client) fetchSessionID() (string, error) {
	status, body, err := c.doRequest("GET",
		"/fh_api/tmp/FHNCAPIS?ajaxmethod=get_refresh_sessionid", "", "login.html", "")
	if err != nil {
		return "", fmt.Errorf("请求 get_refresh_sessionid: %w", err)
	}
	if debugLogin {
		log.Printf("[DEBUG] get_refresh_sessionid status=%d body=%s", status, truncate(body, 200))
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("sessionid 响应为空(status=%d)", status)
	}
	var result struct {
		SessionID string `json:"sessionid"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return "", fmt.Errorf("解析 sessionid (body=%s): %w", truncate(body, 80), err)
	}
	if len(result.SessionID) < 16 {
		return "", fmt.Errorf("sessionid 长度异常: %d", len(result.SessionID))
	}
	return result.SessionID, nil
}

// encryptedCall 按路由器协议完成一次加密请求的全部步骤：
//  1. 调 get_refresh_sessionid 拿到新 sid
//  2. key = DeriveAESKey(sid, magic)
//  3. plainFactory(sid) 生成明文 JSON（允许调用方把 sid 嵌到字段里）
//  4. AES-CBC 加密、hex、前缀 6 字符
//  5. POST urlPath?_<rand>，Content-Type application/json; charset=utf-8
//  6. 响应若 "<6 字符前缀>+hex 密文" 则用同一 key 解密
func (c *Client) encryptedCall(method, urlPath string, plainFactory func(sid string) (string, error), referer string) (int, string, error) {
	if c.magic == "" {
		return 0, "", fmt.Errorf("magic 未初始化（登录未完成）")
	}
	sid, err := c.fetchSessionID()
	if err != nil {
		return 0, "", err
	}
	aesKey, err := crypto.DeriveAESKey(sid, c.magic)
	if err != nil {
		return 0, "", fmt.Errorf("KDF 派生 AES key: %w", err)
	}
	plainJSON, err := plainFactory(sid)
	if err != nil {
		return 0, "", fmt.Errorf("构造 %s 明文: %w", method, err)
	}
	hexCipher, err := crypto.Encrypt(plainJSON, aesKey)
	if err != nil {
		return 0, "", fmt.Errorf("AES 加密: %w", err)
	}
	body := crypto.RandomPrefix() + hexCipher
	fullPath := fmt.Sprintf("%s?_%s", urlPath, randSuffix())

	status, raw, err := c.doRequest("POST", fullPath, body, referer, "application/json; charset=utf-8")
	if debugLogin {
		log.Printf("[DEBUG] POST %s status=%d plain=%s", urlPath, status, truncate(plainJSON, 200))
	}
	if err != nil {
		return status, raw, err
	}
	decoded := tryDecryptResponse(strings.TrimSpace(raw), aesKey)
	return status, decoded, nil
}

// tryDecryptResponse 尝试按路由器加密响应协议解码：
//   - 前 6 个字符为随机前缀（与请求 body 同规则），剩余为 hex 密文；
//   - 若整段看起来不是 "前缀 + hex"，则视为明文原样返回。
func tryDecryptResponse(raw, key string) string {
	if len(raw) < 32+6 { // 至少一块密文 + 前缀
		return raw
	}
	hexPart := raw[6:]
	if !looksLikeHex(hexPart) || len(hexPart)%32 != 0 {
		return raw
	}
	dec, err := crypto.Decrypt(hexPart, key)
	if err != nil {
		return raw
	}
	return dec
}

// Login 执行完整登录流程（见包注释）。
func (c *Client) Login() error {
	// 1. 预取 login.html，部分固件据此下发 cookie / 初始化会话上下文。
	//    失败不阻断，直接走后续流程也能登录成功。
	_, _, _ = c.doRequest("GET", "/login.html", "", "", "")

	// 2. RSA 协商：取回 magic 字符串，全会话复用
	magic, err := c.fetchMagic()
	if err != nil {
		return err
	}
	c.magic = magic

	// 3. 发起登录请求。encryptedCall 会自动 refresh sid + 派生 key。
	status, respBody, err := c.encryptedCall(
		"DO_WEB_LOGIN",
		"/fh_api/sign/DO_WEB_LOGIN",
		func(sid string) (string, error) {
			return buildLoginPlaintext(c.username, c.password, sid), nil
		},
		"login.html",
	)
	if err != nil {
		return fmt.Errorf("登录请求: %w", err)
	}
	if debugLogin {
		log.Printf("[DEBUG] DO_WEB_LOGIN status=%d resp=%s", status, truncate(respBody, 300))
	}

	var result struct {
		Result int `json:"result"`
		User   int `json:"user"`
	}
	if err := json.Unmarshal([]byte(respBody), &result); err != nil {
		return fmt.Errorf("解析登录结果 (status=%d body=%s): %w", status, truncate(respBody, 120), err)
	}
	if result.Result != 0 {
		return fmt.Errorf("登录失败: result=%d, user=%d (%s)", result.Result, result.User, loginErrText(result.Result))
	}

	log.Printf("CPE 登录成功")
	return nil
}

// buildLoginPlaintext 手工拼 JSON 以保证字段顺序恒为 dataObj→ajaxmethod→sessionid。
// 用户名/密码在 dataObj 内部，Go 的 json.Marshal 对 map 会按 key 排序，因此用
// encoding/json.Marshal 无法保证顺序；直接字符串拼接最可靠。
func buildLoginPlaintext(username, password, sid string) string {
	u, _ := json.Marshal(username)
	p, _ := json.Marshal(password)
	s, _ := json.Marshal(sid)
	return fmt.Sprintf(`{"dataObj":{"username":%s,"password":%s},"ajaxmethod":"DO_WEB_LOGIN","sessionid":%s}`,
		string(u), string(p), string(s))
}

// buildAPIPlaintext 为非登录加密接口拼明文。dataObj 可为 nil / 空对象。
func buildAPIPlaintext(method string, dataObj map[string]any, sid string) (string, error) {
	var dataJSON string
	if dataObj == nil {
		dataJSON = "{}"
	} else {
		b, err := json.Marshal(dataObj)
		if err != nil {
			return "", err
		}
		dataJSON = string(b)
	}
	m, _ := json.Marshal(method)
	s, _ := json.Marshal(sid)
	return fmt.Sprintf(`{"dataObj":%s,"ajaxmethod":%s,"sessionid":%s}`,
		dataJSON, string(m), string(s)), nil
}

// FetchSMS 获取短信列表。
func (c *Client) FetchSMS() ([]SMSSession, error) {
	status, body, err := c.encryptedCall(
		"get_sms_data",
		"/fh_api/tmp/FHAPIS",
		func(sid string) (string, error) {
			return buildAPIPlaintext("get_sms_data", nil, sid)
		},
		"main.html",
	)
	if err != nil {
		return nil, fmt.Errorf("SMS 请求: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("SMS 请求返回 %d (body=%s)", status, truncate(body, 120))
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("SMS 响应为空（可能会话已失效）")
	}

	return parseSMSResponse(body)
}

// Heartbeat 发送心跳保持会话。
// 心跳是非加密 GET，固件返回纯文本 "1"。
func (c *Client) Heartbeat() error {
	status, body, err := c.doRequest("GET", "/fh_api/tmp/heartbeat", "", "main.html", "")
	if err != nil {
		return err
	}
	if status != 200 || !strings.Contains(body, "1") {
		return fmt.Errorf("心跳失败: status=%d body=%s", status, truncate(body, 80))
	}
	return nil
}

// OtherLogged 查询路由器全局 Web 会话占用状态。
//
// 这是登录页 login.js 中 onApply 先行调用的 $get("other_logged") 所对应的接口：
// 明文 GET /fh_api/tmp/other_logged，无需登录、不会顶号。固件返回
// {"result":1,"login_ip":"x.x.x.x"} 表示当前已有连接持有会话（result!=1 表示空闲）。
// 据此可在登录前判断后台是否正被其他设备占用，从而自动退避而非贸然顶号。
//
// occupied=true 表示有连接持有会话，holderIP 为持有者源 IP（可能为空）。
func (c *Client) OtherLogged() (occupied bool, holderIP string, err error) {
	status, body, err := c.doRequest("GET", "/fh_api/tmp/other_logged", "", "login.html", "")
	if err != nil {
		return false, "", fmt.Errorf("请求 other_logged: %w", err)
	}
	if status != 200 {
		return false, "", fmt.Errorf("other_logged 返回 status=%d body=%s", status, truncate(body, 80))
	}
	var r struct {
		Result  int    `json:"result"`
		LoginIP string `json:"login_ip"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &r); err != nil {
		return false, "", fmt.Errorf("解析 other_logged (body=%s): %w", truncate(body, 80), err)
	}
	return r.Result == 1, r.LoginIP, nil
}

func parseSMSResponse(decrypted string) ([]SMSSession, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(decrypted), &raw); err != nil {
		return nil, fmt.Errorf("解析 SMS JSON (body=%s): %w", truncate(decrypted, 120), err)
	}

	// 兼容两种返回结构：
	// 1. 新固件：直接平铺 session_item_* 在最外层
	// 2. 旧固件：包在 data_2 下
	source := raw
	if data2Raw, ok := raw["data_2"]; ok {
		var data2 map[string]json.RawMessage
		if err := json.Unmarshal(data2Raw, &data2); err == nil {
			source = data2
		}
	}

	var sessions []SMSSession
	for key, val := range source {
		if !strings.HasPrefix(key, "session_item_") {
			continue
		}
		var sessionData map[string]json.RawMessage
		if err := json.Unmarshal(val, &sessionData); err != nil {
			continue
		}

		session := SMSSession{}
		if phoneRaw, ok := sessionData["session_phone"]; ok {
			_ = json.Unmarshal(phoneRaw, &session.Phone)
		}

		for msgKey, msgVal := range sessionData {
			if !strings.HasPrefix(msgKey, "message_item_") {
				continue
			}
			var msg SMSMsg
			if err := json.Unmarshal(msgVal, &msg); err != nil {
				continue
			}
			session.Messages = append(session.Messages, msg)
		}
		if len(session.Messages) > 0 {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

// loginErrText 映射 DO_WEB_LOGIN 的 result 错码为中文文案：1..4 对照固件
// lang/login_res.js 的 loginErr1..4（1=已在别处登录、2=连续登录错误过多、
// 3=管理账号被禁用、4=用户名或密码错误）；9 为实测出现的会话/加密协商失败码，
// 其余 result 一律归为未知错误。
func loginErrText(code int) string {
	switch code {
	case 1:
		return "当前已有用户在别处登录，请稍后登录"
	case 2:
		return "连续登录错误次数过多，请 1 分钟后再试"
	case 3:
		return "管理账号已被禁用，请另选账号登录"
	case 4:
		return "用户名或密码错误"
	case 9:
		return "未知错误（可能为会话/加密协商失败或被风控）"
	default:
		return "未知错误"
	}
}

// randSuffix 对应 JS 里 Math.random() 的近似表示，仅用于为 URL 增加随机 _
// 查询参数，避免浏览器 / 中间代理对 GET/POST 做不合适的缓存。
func randSuffix() string {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		// 该后缀仅用于给 URL 加随机查询参数防缓存，非安全敏感；强随机不可用时退回纳秒时间。
		return fmt.Sprintf("0.%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("0.%d", binary.BigEndian.Uint64(b[:])%uint64(1e16))
}

func looksLikeHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

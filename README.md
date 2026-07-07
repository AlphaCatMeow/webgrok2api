# grok2api

将 Grok (x.ai) 转换为 OpenAI / Anthropic 兼容的 API 网关。纯 Go 实现，单二进制部署，多架构 Docker 镜像开箱即用。

## 功能特性

- **OpenAI 兼容** — `/v1/chat/completions`、`/v1/images/generations`、`/v1/videos`、`/v1/responses`
- **Anthropic 兼容** — `/v1/messages`，支持流式和非流式
- **多账号池管理** — 支持 basic / super / heavy 三级账号池，自动配额跟踪
- **智能选号** — 配额感知策略（按剩余配额评分）和随机策略，自动故障转移
- **浏览器指纹伪装** — TLS 指纹、HTTP/2 头序、Chrome 客户端提示，规避上游检测
- **WebSocket 图像生成** — 通过 `wss://grok.com/ws/imagine/listen` 实时流式生成图像，支持进度回调
- **纯 Go 生成反 bot 头** — `x-statsig-id` 由内置 CSS 动画指纹算法实时生成（启动时自动创建随机 seed + HEX），无需浏览器或 JS 运行时
- **代理支持** — 直连 / 单代理，兼容 HTTP/HTTPS/SOCKS4/5
- **Cloudflare 绕过** — 手动 Cookie 注入，`cf_clearance` 自动提取
- **本地媒体缓存** — 图片和视频本地缓存，LRU 淘汰
- **管理后台** — 完整的 Token CRUD、配置热更新、批量操作
- **热重载配置** — 修改配置文件即时生效，无需重启
- **多实例部署** — 基于文件锁的 Leader 选举，支持多进程运行
- **多架构 Docker 镜像** — GHCR 自动构建 amd64 / arm64 / armv7

## 快速开始

> **Docker 用户**：可直接 `docker pull ghcr.io/aurora-develop/grok2api:latest` 一键启动，无需编译。完整 Docker 指引见文末[部署](#部署)章节；下方流程适用于源码运行。

### 1. 获取 SSO Token

SSO Token 是你的 Grok 账号凭证，用于调用上游 Grok API。

**方法一：浏览器 DevTools（推荐）**

1. 打开 [grok.com](https://grok.com) 并登录你的账号
2. 按 `F12` 打开浏览器开发者工具
3. 切换到 **Application**（应用程序）标签页
4. 左侧找到 **Cookies** → `https://grok.com`
5. 找到名为 `sso` 的 Cookie，复制它的 **Value** 值
6. 这个值就是你的 SSO Token（通常是一串很长的字符）

**方法二：Network 面板抓包**

1. 打开 [grok.com](https://grok.com) 并登录
2. 按 `F12` → **Network**（网络）标签页
3. 在 Grok 页面随便发一条消息
4. 找到 `conversations/new` 请求 → **Headers** → **Cookie**
5. 从 Cookie 字符串中提取 `sso=` 后面的值

> **注意**：每个 SSO Token 对应一个 Grok 账号。Token 过期后需要重新获取。免费账号（basic pool）和付费账号（super/heavy pool）的配额不同。

### 2. 编译运行

```bash
# 编译
go build -o grok2api .

# 运行（默认监听 0.0.0.0:8000）
./grok2api
```

或直接运行：

```bash
go run .
```

### 3. 添加账号

通过管理 API 将 SSO Token 添加到账号池：

```bash
# 添加到 basic 池（免费账号）
curl -X POST http://localhost:8000/admin/api/tokens/add \
  -H "Content-Type: application/json" \
  -d '{"tokens": ["你的sso-token"], "pool": "basic"}'

# 添加到 super 池（付费账号）
curl -X POST http://localhost:8000/admin/api/tokens/add \
  -H "Content-Type: application/json" \
  -d '{"tokens": ["你的sso-token"], "pool": "super"}'
```

> 默认管理密码是 `grok2api`（配置项 `app.app_key`）。如果配置了 `app.api_key`，管理 API 也需要在 Header 中带上 `Authorization: Bearer grok2api`。

### 4. 调用 API

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-4.20-0309-non-reasoning",
    "messages": [{"role": "user", "content": "你好！"}],
    "stream": true
  }'
```

也可以直接用 SSO Token 当 Bearer 调用（无需先加入账号池）：

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer 你的sso-token" \
  -d '{
    "model": "grok-4.20-0309-non-reasoning",
    "messages": [{"role": "user", "content": "你好！"}],
    "stream": true
  }'
```

> **鉴权规则**：默认 `api_key` 为空，完全开放。配置 `api_key` 后，请求必须携带匹配的 API Key 或任意 SSO Token。

### 对接 OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8000/v1", api_key="any")

response = client.chat.completions.create(
    model="grok-4.20-0309-non-reasoning",
    messages=[{"role": "user", "content": "你好！"}],
)
print(response.choices[0].message.content)
```

### 对接 Anthropic SDK

```python
import anthropic

client = anthropic.Anthropic(base_url="http://localhost:8000", api_key="any")

message = client.messages.create(
    model="grok-4.20-0309-non-reasoning",
    max_tokens=4096,
    messages=[{"role": "user", "content": "你好！"}],
)
print(message.content[0].text)
```

## 配置反爬绕过（重要）

Grok 有 Cloudflare + 自研反爬机制。要正常调用，需要从浏览器抓取两组凭证：

### 第一步：抓取 Cloudflare 凭证

1. 打开 [grok.com](https://grok.com)（已登录），F12 → **Network**
2. 在 Grok 页面随便发一条消息
3. 找到 `conversations/new` 请求 → **Headers** → **Cookie**
4. 复制 `cf_clearance` 的值

### 第二步：抓取 grok 会话令牌

`x-anonuserid`、`x-challenge`、`x-signature`、`x-userid` 是 grok 服务器通过 `Set-Cookie` 响应头下发的会话令牌，**不是客户端生成的**。它们与 `cf_clearance` 必须来自同一个浏览器会话。

**抓取方法：**

1. 浏览器打开 [grok.com](https://grok.com) 并登录
2. F12 → **Application**（应用程序）标签页
3. 左侧 **Cookies** → `https://grok.com`
4. 依次找到并复制以下 4 个 Cookie 的值：
   - `x-anonuserid` — 匿名用户 UUID（首次访问时服务器自动下发）
   - `x-userid` — 登录用户 UUID
   - `x-challenge` — grok proof-of-work 挑战令牌（较长的 base64 字符串）
   - `x-signature` — 对 challenge 的签名（较短的 base64 字符串）

> **注意**：这 4 个令牌是**服务器绑定的会话凭证**，过期后无法客户端续期，必须重新登录获取。

### 第三步：填写配置

将抓取的值填入 `data/config.toml`：

```toml
[proxy.clearance]
# 整段 Cookie 也可以直接粘贴（程序会自动提取所需部分）
cf_cookies = "cf_clearance=..."
user_agent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"
# 从浏览器 Cookies 中逐个复制
x_anonuserid = "717688f6-ea07-4d30-ba7c-af3626e8ab78"
x_userid = "2a0af403-875b-4f0f-b5d7-c77171a14fd2"
x_challenge = "Zb6xK8xUxi%2BSOyI7k%2FGZz6beN7VuzJh3hsUWaHoxzLhEb5Jg1eq3T25zgU527pDEFDSukZPsH0Gn%2FBgYC4yK2K..."
x_signature = "5H1cUg2b%2BAJJCeV7jA%2BnumHP4s3i4ercMPLv6OlDuvl1gd12J1Zc3Q%2FXcd7OQ0%2FSaB9upVHGQigfMdc%2BjyZAuQ%3D%3D"
```

> **提示**
> - `cf_clearance` 有效期通常几小时到一天，过期后需重新抓取，否则会返回 403。
> - `x-challenge` / `x-signature` / `x-anonuserid` / `x-userid` 是 grok 内部匿名会话的 anti-bot 令牌，**必须与 `cf_clearance` 来自同一浏览器会话**。如果你更新了其中一个，建议全部重新导出。
> - `x-statsig-id` 反 bot 头由纯 Go 实时自动生成（启动时自动创建随机 seed + 内置 HEX 算法），**无需手动配置**。如果被 grok 拒绝（code:7），可调用 `RotatePair()` 刷新。

### 抓取 statsig 指纹

程序内置纯 Go 自动生成 `x-statsig-id`（启动时创建随机 seed，通过 CSS 动画指纹算法计算 HEX），**大多数场景无需额外配置**。

如果 grok 频繁返回 `code:7`（anti-bot 拒绝），说明当前 seed/HEX 对可能被标记。程序会自动尝试 `RotatePair()` 刷新。如仍失败，可手动抓取浏览器真实值：

**步骤：**

1. 打开 [grok.com](https://grok.com) 并登录
2. 按 `F12` → **Console**（控制台）
3. 粘贴并执行以下脚本：

```javascript
(()=>{const o=crypto.subtle.digest.bind(crypto.subtle);crypto.subtle.digest=function(a,d){const s=new TextDecoder().decode(new Uint8Array(d.buffer||d)),i=s.indexOf('obfiowerehiring');if(i>=0)console.log('SEED=',document.querySelector('meta[name="grok-site―verification"]').content,'\nHEX=',s.slice(i+15));return o(a,d);};})();
```

4. 在 Grok 页面**发送一条消息**
5. 控制台会输出 `SEED=...` 和 `HEX=...`
6. 将 `SEED` 和 `HEX` 填入 `data/config.toml`：

```toml
[proxy.clearance]
statsig_seed = "abc123def456"
statsig_hex  = "0123456789abcdef"
```

## 配置说明

配置文件采用 TOML 格式，加载优先级：

1. `config.defaults.toml`（内置默认值）
2. `data/config.toml`（用户自定义，覆盖默认值）
3. `GROK_*` 环境变量（最高优先级）

### 主要配置项

```toml
[app]
app_key = "grok2api"           # 管理后台密码
api_key = ""                    # API 密钥（留空不鉴权，逗号分隔多个）

[logging]
file_level = "INFO"             # 文件日志级别
max_files = 7                   # 日志文件最大保留数

[features]
stream = true                   # 默认流式响应
thinking = true                 # 输出思考过程
temporary = true                # 临时对话（不保存历史）
memory = false                  # 会话记忆
auto_chat_mode_fallback = true  # AUTO 模型自动降级到 fast/expert
custom_instruction = ""         # 全局附加指令（系统提示）

[cache.local]
image_max_mb = 0                # 图片缓存上限（MB），0 = 不限制
video_max_mb = 0                # 视频缓存上限（MB），0 = 不限制

[proxy.egress]
proxy_url = ""                  # 出站代理（留空直连），HTTP/HTTPS/SOCKS4/5

[proxy.clearance]
cf_cookies = ""                 # 手动模式：浏览器 Cookie 串（含 cf_clearance）
user_agent = "..."              # 需与抓取 Cookie 时的 UA 一致
statsig_seed = ""               # 可选：手动覆盖自动 statsig 种子
statsig_hex  = ""               # 可选：手动覆盖自动 statsig HEX 指纹
statsig_from_html = false       # 已弃用：程序现已内置纯 Go 自动生成功能

[retry]
max_retries = 1                 # 换账号重试最大次数（0 = 不重试）
on_codes = "429,401,503"        # 触发重试的 HTTP 状态码
reset_session_status_codes = [403]  # 触发重建代理 Session 的状态码

[account.refresh]
enabled = true                  # true=配额模式；false=随机模式
basic_interval_sec = 86400      # basic 池刷新间隔（秒）
super_interval_sec = 7200       # super 池刷新间隔（秒）
heavy_interval_sec = 7200       # heavy 池刷新间隔（秒）

[account.selection]
max_inflight = 8                # 单号并发上限

[asset]
upload_timeout = 60             # 资源上传超时（秒）
list_timeout = 60               # 资源列表超时（秒）
delete_timeout = 60             # 资源删除超时（秒）

[nsfw]
timeout = 60                    # NSFW 设置超时（秒）
```

> `config.defaults.toml` 内置全部默认值，`data/config.toml` 只需覆盖你想修改的项即可。

### 环境变量

| 变量 | 说明 | 默认值 |
|---|---|---|
| `SERVER_HOST` | 监听地址 | `0.0.0.0` |
| `SERVER_PORT` | 监听端口 | `8000` |
| `LOG_LEVEL` | 日志级别 | `INFO` |
| `LOG_FILE_ENABLED` | 启用文件日志 | `true` |
| `DATA_DIR` | 数据目录 | `./data` |
| `ACCOUNT_LOCAL_PATH` | 账号存储路径 | `./data/accounts.jsonl` |
| `PROXY_HTTP` | 代理地址（覆盖配置文件） | _(空)_ |
| `GROK_SECTION_KEY` | 配置覆盖（映射到 `section.key`） | _(空)_ |

> `GROK_*` 环境变量可用于覆盖任意配置项。例如 `GROK_FEATURES_STREAM=false` 等同于 `features.stream = false`。

## 账号池与模型

### 账号池

| 池 | 说明 | 配额周期 |
|---|---|---|
| basic | 免费账号 | 24 小时 |
| super | 付费账号 | 2 小时 |
| heavy | 高级账号 | 2 小时 |

### 可用模型

**grok.com 聊天**：`grok-4.20-0309`、`grok-4.20-0309-reasoning`、`grok-4.20-heavy`、`grok-4.20-multi-agent-0309` 等 16 个模型

**Console**：`grok-4.3-console`、`grok-4.3-high`、`grok-4.20-multi-agent-xhigh`、`grok-4.20-0309-non-reasoning-console`、`grok-build-console` 等 13 个模型（通过 console.x.ai，免费额度）

**媒体**：`grok-imagine-image-lite`、`grok-imagine-image`、`grok-imagine-image-pro`（WebSocket 实时生成）、`grok-imagine-image-edit`、`grok-imagine-video`

完整模型列表见 [API.md](API.md)。

## 管理 API

管理端点使用 `app.app_key` 认证，支持 `Authorization: Bearer` 或 `?app_key=` 参数。

```bash
# 查看系统状态
curl http://localhost:8000/admin/api/status \
  -H "Authorization: Bearer grok2api"

# 查看所有 Token
curl http://localhost:8000/admin/api/tokens \
  -H "Authorization: Bearer grok2api"

# 更新配置
curl -X POST http://localhost:8000/admin/api/config \
  -H "Authorization: Bearer grok2api" \
  -H "Content-Type: application/json" \
  -d '{"key": "features.thinking", "value": "false"}'
```

完整管理 API 文档见 [API.md](API.md)。

## 部署

### Docker（推荐）

镜像已通过 GitHub Actions 自动构建并发布到 GHCR，支持 `linux/amd64`、`linux/arm64`、`linux/arm/v7` 多架构。直接拉取即可使用，无需本地编译。

```bash
# 拉取最新镜像
docker pull ghcr.io/aurora-develop/grok2api:latest

# 运行容器（挂载 data 目录以持久化配置与账号数据）
docker run -d \
  --name grok2api \
  -p 8000:8000 \
  -v $(pwd)/data:/app/data \
  ghcr.io/aurora-develop/grok2api:latest
```

启动后访问 `http://localhost:8000`，管理后台密码默认为 `grok2api`。

> **可选环境变量**：`SERVER_PORT`（覆盖监听端口）、`PROXY_HTTP`（覆盖出站代理）、`TZ`（时区，如 `Asia/Shanghai`）。
>
> **指定版本**：`docker pull ghcr.io/aurora-develop/grok2api:v1.0.1`，或用 commit 短哈希 `ghcr.io/aurora-develop/grok2api:<sha>` 锁定具体构建。

#### Docker Compose

```yaml
services:
  grok2api:
    image: ghcr.io/aurora-develop/grok2api:latest
    container_name: grok2api
    restart: unless-stopped
    ports:
      - "8000:8000"
    volumes:
      - ./data:/app/data
    environment:
      - TZ=Asia/Shanghai
      # - PROXY_HTTP=http://127.0.0.1:7890
```

```bash
docker compose up -d
```

#### 本地自行构建

如需修改源码后自建镜像，可使用仓库自带的 `Dockerfile`：

```bash
docker build -t grok2api .
docker run -p 8000:8000 -v ./data:/app/data grok2api
```

### 多实例

支持多进程部署，Leader 进程负责配额刷新，Follower 进程只做增量同步。基于 `flock` 文件锁自动选举。


## 常见问题

**Q: 如何获取 SSO Token？**
A: 登录 [grok.com](https://grok.com)，按 F12 打开开发者工具 → Application → Cookies → 找到 `sso` 字段复制其值。详见上方「获取 SSO Token」章节。

**Q: 为什么调用返回 403 "Request rejected by anti-bot rules"？**
A: 这是 Grok 的反爬机制，通常是 `cf_clearance` 缺失或过期。解决方法：

1. 按上方「配置反爬绕过」重新抓取浏览器 Cookie，填入 `proxy.clearance.cf_cookies`
2. 确保 `proxy.clearance.user_agent` 与抓取时浏览器的 UA 完全一致
3. 若使用代理，确认代理出口 IP 与浏览器 IP 一致（cf_clearance 绑定 IP）

> `x-statsig-id` 等反 bot 头由程序自动生成，无需手动处理。推荐启用 `statsig_from_html = true` 以自动使用动态 HTML 种子 + SVG 路径表生成 HEX。如遇频繁拦截且未启用自动模式，可手动抓取真实值，详见[抓取 statsig 指纹](#抓取-statsig-指纹)。

**Q: 如何获取 cf_clearance？**
A: 登录 grok.com，F12 → Network → 刷新页面 → 找到任意 grok.com 请求 → Request Headers → Cookie → 复制 `cf_clearance=...` 的值。有效期通常几小时到一天。最简单的做法是把整段 Cookie 头都粘进 `cf_cookies`。

**Q: 如何使用代理？**
A: 在 `config.toml` 的 `[proxy.egress]` 中设置 `proxy_url`，兼容 HTTP/HTTPS/SOCKS4/SOCKS5 协议；或用环境变量 `PROXY_HTTP` 覆盖。示例：`proxy_url = "socks5://127.0.0.1:1080"`。

**Q: 支持图片输入吗？**
A: 支持。在 messages 的 content 中使用 `image_url` 类型，支持 URL 和 base64 data URI。

**Q: 多实例怎么部署？**
A: 直接启动多个进程，自动通过文件锁选举 Leader。Leader 负责配额刷新，所有进程都处理 API 请求。Docker 多实例同理，分别 `docker run` 即可（注意挂载各自的 `data` 目录或共享存储）。

## 致谢

本项目在以下开源项目的基础上发展而来，特此致谢：

- [chenyme/grok2api](https://github.com/chenyme/grok2api) — 原始 Python 实现，为本项目的协议兼容与账号管理提供了重要参考。

同时感谢 [LINUX DO 社区](https://linux.do) —— 本项目在此发布，感谢社区用户的反馈与帮助。

> 本项目为独立重写的 Go 实现，与上述项目无附属关系，旨在提供更轻量、高性能的部署体验。

## 许可

MIT License

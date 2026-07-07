# Grok x-statsig-id 逆向分析报告

日期: 2026-07-07

## 1. 概述

`x-statsig-id` 是 grok.com 用于 anti-bot 验证的 HTTP 头部字段。每次 API 请求都需要携带一个 70 字节（base64 编码后 94 字符）的 statsig ID，grok 服务端会校验其内部一致性。

本报告记录了从 grok.com 当前构建版本中完整逆向该算法的过程，最终实现了纯 Go 自动生成，无需浏览器或 JS 运行时。

## 2. x-statsig-id 结构

```
字节布局（70 字节）:
  [0]      = 随机 XOR key（1 字节）
  [1..48]  = seed[0..47] XOR key    （48 字节）
  [49..52] = uint32LE(number) XOR key （4 字节）
  [53..68] = SHA-256[0:16] XOR key   （16 字节）
  [69]     = 0x03 XOR key            （1 字节）

输出: base64.RawStdEncoding(out) → 94 字符字符串
```

### 关键参数
- **epoch**: `0x644f6370` = 1682924400
- **salt**: `"obfiowerehiring"`（RC4 混淆存储，运行时解码）
- **mark**: `0x03`

### SHA-256 输入格式
```
METHOD + "!" + PATH + "!" + number + "obfiowerehiring" + HEX
```
其中 `number = floor(now_unix) - epoch`

## 3. HEX 的生成算法

HEX 是整个逆向中最复杂的部分。它不是一个简单的 SVG 路径数字转换，而是一个 **CSS 动画指纹**，涉及 Web Animations API 和 `getComputedStyle`。

### 3.1 选择器

每个构建版本有一个 CSS-in-JS 哈希类名作为选择器：

| 构建版本 | 选择器 |
|---------|--------|
| 早期 | `.r-bx02o` |
| 2026-07-06 | `.r-9iho9s` |
| 2026-07-07 | `.r-4elvdc` |

该选择器匹配启动画面（splash screen）的 `<svg>` 元素，使用 `document.querySelectorAll(selector)` 获取。

### 3.2 SVG 路径提取

```javascript
querySelectorAll(".r-4elvdc")[seed[5] % 4]
  .childNodes[0].childNodes[1].getAttribute("d")
```

每个 SVG 元素包含一个 `<g>` 子节点，其第一个 `<path>` 子节点有 `d` 属性。

### 3.3 数字解析

```javascript
d.substring(9).split("C").map(seg =>
  seg.replace(/[^\d]+/g, " ").trim().split(" ").filter(Boolean).map(Number)
)
```

结果是 `C` — 一个二维数字数组，每个 segment 是一组数字。

### 3.4 Seed 派生参数

```javascript
i = seed[5] % 16                    // 选择 C 的 segment 索引
a = (seed[24]%16) * (seed[22]%16) * (seed[23]%16)  // 动画时间参数
currentTime = Math.round(a / 10) * 10               // 动画 seek 时间（毫秒）
```

### 3.5 CSS 动画指纹

这是核心算法，通过 Web Animations API + getComputedStyle 生成 HEX：

```javascript
// 从 C[i] 构建 keyframes
color0 = "#" + hex(C[i][0]) + hex(C[i][1]) + hex(C[i][2])
color1 = "#" + hex(C[i][3]) + hex(C[i][4]) + hex(C[i][5])
angle  = scale(C[i][6], 60, 360, floor=true)
x1, y1, x2, y2 = scale(C[i][7..10], {-1,0,1})

// 创建动画
anim = element.animate({
  color:     [color0, color1],
  transform: ["rotate(0deg)", "rotate(" + angle + "deg)"],
  easing:    "cubic-bezier(" + x1 + "," + y1 + "," + x2 + "," + y2 + ")"
}, { duration: 4096 });

// 暂停并 seek
anim.pause();
anim.currentTime = currentTime;

// 读取 Chrome 归一化后的 CSS 计算样式
style = getComputedStyle(element);

// 提取数字并转 hex
HEX = (style.color + style.transform)
  .matchAll(/([\d.-]+)/g)
  .map(x => Number(Number(x[0]).toFixed(2)).toString(16))
  .join("")
  .replace(/[.-]/g, "");
```

### 3.6 辅助函数

```javascript
scale(n, min, max, floor):
  v = n * ((max - min) / 255) + min
  if floor: Math.floor(v)
  else: Number(v.toFixed(2))
```

### 3.7 实测验证

同一构建版本、不同 seed 的实测结果（证明 HEX 是 seed 依赖的）：

| seed (base64) | seed[5] | seed[5]%4 | HEX |
|---|---|---|---|
| `Jf/kUB64jY1...` | 184 | 0 | `50a74b0f33333333333304f5c28f5c28f5c04f5c28f5c28f5c0f33333333333300` |
| `D3zbtvwwXHf1...` | 48 | 0 | `1a2aa30fd70a3d70a3d7028f5c28f5c28f6028f5c28f5c28f60fd70a3d70a3d700` |
| `t2ODAFY4ozXd0...` | 56 | 1 | `3bab9506b851eb851eb840e8f5c28f5c28f80e8f5c28f5c28f806b851eb851eb8400` |
| `SqC7PDHvzHsRS...` | 239 | 3 | `958d330e147ae147ae14807ae147ae147ae07ae147ae147ae0e147ae147ae14800` |

## 4. 混淆层解码

### 4.1 W(n, r) 函数

代码块中的字符串通过 `W(n, r)` 函数解码：

1. `t()` 返回一个字符串数组（混淆器表）
2. 数组在模块加载时经过 142 步的自旋转（`t.push(t.shift())`，直到校验和等于 155763）
3. `W(n, r)` 使用 `n - 381` 作为索引，取出 `t[index]`
4. 通过 RC4 解密（key = `r`），使用自定义 base64 字母表解码

关键解码结果：

| 调用 | 解码结果 |
|---|---|
| `gcfub` = `W(443,"NDU%")+W(649,"*8yX")` | `.r-9iho9s` (选择器) |
| `qNSMw` = `W(641,"*8yX")+W(550,"ht&6")+W(661,"*8yX")` | `obfiowerehiring` (salt) |
| `childNodes` | `childNodes` |
| `split` / `map` / `replace` / `trim` | 对应原生方法名 |
| `getComputedStyle` | `getComputedStyle` |
| `animate` | `animate` |

### 4.2 E(seed) 中 seed 的使用

`E(seed)` 函数使用了多个 seed 字节：

| 字节 | 用途 |
|---|---|
| `seed[5]` | `% 4` 选择 SVG 元素；`% 16` 选择 segment |
| `seed[6]` | `parseInt(seed[6], 16)` 选择 SVG 数组索引 |
| `seed[12]` | 参与动画时间计算 |
| `seed[17]` | 参与动画时间计算 |
| `seed[22]` | `% 16` 参与动画时间计算 |
| `seed[23]` | `% 16` 参与动画时间计算 |
| `seed[24]` | `% 16` 参与动画时间计算 |

## 5. 纯 Go 实现

### 5.1 核心算法（svgfingerprint/compute.go）

```go
func computeAnimationHEX(svgPathD string, seed []byte) (string, error) {
    // 1. 解析 SVG 路径数字
    segments := pathNumberSegments(svgPathD)

    // 2. 选择 segment
    segIdx := int(seed[5]) % 16
    seg := segments[segIdx]

    // 3. 提取 keyframe 参数
    startColor := [3]float64{seg[0], seg[1], seg[2]}
    endColor := [3]float64{seg[3], seg[4], seg[5]}
    endAngle := scaleValue(seg[6], 60, 360, true)
    x1 := scaleValue(seg[7], 0, 1, false)
    y1 := scaleValue(seg[8], -1, 1, false)
    x2 := scaleValue(seg[9], 0, 1, false)
    y2 := scaleValue(seg[10], -1, 1, false)

    // 4. 计算动画时间
    seek := math.Round(float64((int(seed[24])%16)*(int(seed[22])%16)*(int(seed[23])%16))/10) * 10

    // 5. 计算 cubic-bezier 进度
    progress := cubicBezierY(x1, y1, x2, y2, seek/4096)

    // 6. 颜色插值 + 旋转矩阵
    r := cssColorChannel(startColor[0], endColor[0], progress)
    g := cssColorChannel(startColor[1], endColor[1], progress)
    b := cssColorChannel(startColor[2], endColor[2], progress)
    angle := endAngle * progress * math.Pi / 180
    cosV, sinV := math.Cos(angle), math.Sin(angle)

    // 7. 组装数值并转 HEX
    values := []float64{float64(r), float64(g), float64(b), cosV, sinV, -sinV, cosV, 0, 0}
    // 每个值: Number(Number(x).toFixed(2)).toString(16)
}
```

### 5.2 自启动（pure.go）

```go
func init() {
    seed := freshSeed()              // 随机 48 字节
    curSeed = seed
    curHEX = freshHEX(seed)          // ComputeHEXForSeed(seed)
}
```

每次进程启动自动生成一对新的 (seed, HEX)，无需硬编码或浏览器。

### 5.3 E2E 验证

- `TestE2E_ShardCrosscheck`：Go 计算 HEX 与浏览器实测完全一致
- `/v1/chat/completions`：HTTP 200，支持 `grok-4.20-fast` 和 `grok-4.20-0309-non-reasoning`

## 6. 关键发现

1. **HEX 不是 `f(seed[5]%4)`**：相同 `seed[5]%4=0` 的两个不同 seed 产生了不同的 HEX
2. **HEX 是 CSS 动画指纹**：通过 `Element.animate()` + `getComputedStyle()` 计算，涉及颜色插值和旋转矩阵
3. **HEX 是 seed 依赖的**：使用 seed[5], seed[6], seed[12], seed[17], seed[22], seed[23], seed[24] 七个字节
4. **纯 Go 可复现**：CSS 插值和 cubic-bezier 求解是确定性数学运算，可以完全在 Go 中实现
5. **grok 不校验 seed 与服务器下发的一致性**：只要 (seed, HEX) 内部自洽即可通过

## 7. 配置要求

除了自动生成的 statsig，还需要在 `data/config.toml` 中配置浏览器会话 cookie：

| 字段 | 来源 | 必填 |
|---|---|---|
| `cf_clearance` | Cloudflare 令牌 | ✅ |
| `x-anonuserid` | grok 匿名用户 UUID | ✅ |
| `x-challenge` | grok proof-of-work 挑战 | ✅ |
| `x-signature` | challenge 的 HMAC 签名 | ✅ |
| `x-userid` | 登录用户 UUID | ✅ |

这 5 个字段都是服务器下发的会话凭证，过期后需从浏览器重新导出。

## 8. 已知限制

1. **构建版本变动**：选择器 `.r-4elvdc` 和 SVG 路径是构建特定的，grok 每次部署都可能变化。需要通过 `RotatePair()` 刷新。
2. **Cookie 过期**：`cf_clearance` 和 anti-bot cookie 有时效性，需定期更新。
3. **Minimal headers**：grok 检测额外的浏览器指纹 headers（sentry-trace、Sec-Ch-Ua* 等），必须只发送 Content-Type、User-Agent、Cookie、x-statsig-id。
